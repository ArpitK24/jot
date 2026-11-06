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
	"io/fs"
	"log"
	"net/http"
	"os"
	"reflect"
	"strings"
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

func main() {
	addr := flag.String("addr", "127.0.0.1:8765", "listen address")
	flag.Parse()
	app := NewApp(prefsPath())
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
