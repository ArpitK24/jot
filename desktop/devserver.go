//go:build devserver

// A browser preview of the desktop UI for development:
//
//	go run -tags devserver . [-addr :8765]
//
// It serves frontend/dist and answers the same calls the window makes,
// against the real store (set JOT_HOME to use a scratch one).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
)

var version = "dev"

const shim = `<script>
(function(){
  const call = (n) => async (...a) => {
    const r = await fetch('/call/' + n, {method:'POST', body: JSON.stringify(a)});
    const j = await r.json(); if (j.error) throw new Error(j.error); return j.result;
  };
  const p = new Proxy({}, {get: (_, n) => call(n)});
  window.go = {main: {App: p, Window: p}};
  // Wails events, over a server-sent event stream.
  const subs = {};
  const es = new EventSource('/events');
  es.onmessage = (m) => { const e = JSON.parse(m.data); (subs[e.name] || []).forEach(cb => cb(e.data)); };
  window.runtime = Object.assign(window.runtime || {}, {
    EventsOn: (name, cb) => { (subs[name] = subs[name] || []).push(cb); return () => { subs[name] = subs[name].filter(x => x !== cb); }; },
    EventsOff: (name) => { delete subs[name]; },
  });
})();
</script>`

type devWindow struct{ app *App }

func (w *devWindow) ChooseProject() (string, error) {
	dir := os.Getenv("JOT_DEV_PROJECT")
	if dir == "" {
		return "", nil
	}
	return dir, w.app.AddProjectDir(dir)
}
func (w *devWindow) Confirm(title, message string) bool { return true }
func (w *devWindow) ApplyTheme(string)                  {}
func (w *devWindow) Quit()                              { log.Print("quit requested") }

func main() {
	addr := flag.String("addr", "127.0.0.1:8765", "listen address")
	flag.Parse()
	if v := os.Getenv("JOT_DEV_VERSION"); v != "" {
		version = v // preview the update banner against a pretend build
	}
	if u := os.Getenv("JOT_DEV_RELEASES"); u != "" {
		releasesURL = u
	}
	app := NewApp(prefsPath())
	hub := newHub()
	app.setEmitter(hub.emit)
	http.HandleFunc("/events", hub.serve)
	targets := []reflect.Value{reflect.ValueOf(app), reflect.ValueOf(&devWindow{app})}
	dist, _ := fs.Sub(os.DirFS("."), "frontend/dist")
	files := http.FileServer(http.FS(dist))
	http.HandleFunc("/call/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/call/")
		var raw []json.RawMessage
		json.NewDecoder(r.Body).Decode(&raw)
		reply := map[string]any{}
		var m reflect.Value
		for _, t := range targets {
			if m = t.MethodByName(name); m.IsValid() {
				break
			}
		}
		if !m.IsValid() {
			reply["error"] = "no method " + name
		} else {
			args := make([]reflect.Value, m.Type().NumIn())
			for i := range args {
				v := reflect.New(m.Type().In(i))
				if i < len(raw) {
					json.Unmarshal(raw[i], v.Interface())
				}
				args[i] = v.Elem()
			}
			for _, out := range m.Call(args) {
				if e, ok := out.Interface().(error); ok || out.Type() == reflect.TypeOf((*error)(nil)).Elem() {
					if e != nil {
						reply["error"] = e.Error()
					}
					continue
				}
				reply["result"] = out.Interface()
			}
		}
		json.NewEncoder(w).Encode(reply)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			b, _ := fs.ReadFile(dist, "index.html")
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(strings.Replace(string(b), "<script src=\"app.js\">", shim+"<script src=\"app.js\">", 1)))
			return
		}
		files.ServeHTTP(w, r)
	})
	log.Printf("jot desktop preview on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

// hub fans events out to every open /events stream, the preview's
// stand-in for Wails events.
type hub struct {
	mu   sync.Mutex
	subs map[chan []byte]bool
}

func newHub() *hub { return &hub{subs: map[chan []byte]bool{}} }

func (h *hub) emit(name string, data any) {
	b, err := json.Marshal(map[string]any{"name": name, "data": data})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs {
		select {
		case c <- b:
		default: // a stuck tab must not stall the reply
		}
	}
}

func (h *hub) serve(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	c := make(chan []byte, 256)
	h.mu.Lock()
	h.subs[c] = true
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.subs, c); h.mu.Unlock() }()
	fmt.Fprint(w, ": ok\n\n")
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case b := <-c:
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		}
	}
}
