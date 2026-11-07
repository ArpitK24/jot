package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// client is shared by every provider. It has no overall timeout because a
// reply streams for as long as the model writes; callers bound it with ctx.
var client = &http.Client{}

// post sends a JSON body and returns the response when it is a 200. Any
// other status becomes an *Error carrying the service's message.
func post(ctx context.Context, provider, url string, body any, headers map[string]string) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, &Error{Provider: provider, Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return do(ctx, provider, req)
}

func get(ctx context.Context, provider, url string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &Error{Provider: provider, Message: err.Error()}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := do(ctx, provider, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

func do(ctx context.Context, provider string, req *http.Request) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		host := req.URL.Scheme + "://" + req.URL.Host
		return nil, &Error{Provider: provider, Message: "can't reach " + host + " (is it running?)"}
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return nil, &Error{Provider: provider, Status: resp.StatusCode, Message: errorMessage(raw, resp.Status)}
}

// errorMessage digs the human message out of the error shapes the three
// services use: {"error":"..."}, {"error":{"message":"..."}} and
// {"type":"error","error":{"message":"..."}}.
func errorMessage(raw []byte, fallback string) string {
	var e struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil {
		var s string
		if json.Unmarshal(e.Error, &s) == nil && s != "" {
			return s
		}
		var o struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(e.Error, &o) == nil && o.Message != "" {
			return o.Message
		}
		if e.Message != "" {
			return e.Message
		}
	}
	msg := strings.TrimSpace(string(raw))
	if msg == "" || strings.HasPrefix(msg, "<") {
		return fallback
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}

// sse reads a server-sent event stream and calls fn with each event's name
// and data. fn returns false to stop early.
func sse(r io.Reader, fn func(event, data string) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var event string
	var data []string
	flush := func() bool {
		if len(data) == 0 {
			event = ""
			return true
		}
		ok := fn(event, strings.Join(data, "\n"))
		event, data = "", nil
		return ok
	}
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		switch {
		case line == "":
			if !flush() {
				return nil
			}
		case strings.HasPrefix(line, ":"):
			// comment / keep-alive
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(line[len("data:"):], " "))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	flush()
	return nil
}
