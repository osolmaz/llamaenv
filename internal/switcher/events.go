package switcher

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

// serveEvents merges the /models/sse streams of all routers. Every event names
// its model, so only the owning router's events pass.
func (s *Switcher) serveEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.def.serve(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, b := range s.all() {
		if !b.available() {
			continue
		}
		wg.Add(1)
		go func(b *backend) {
			defer wg.Done()
			s.relayEvents(r.Context(), b, func(event []byte) {
				mu.Lock()
				defer mu.Unlock()
				_, _ = w.Write(event)
				flusher.Flush()
			})
		}(b)
	}
	wg.Wait()
}

func (s *Switcher) relayEvents(ctx context.Context, b *backend, emit func([]byte)) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.url("/models/sse"), nil)
	if err != nil {
		return
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	readEvents(resp.Body, func(event []byte, model string) {
		if model == "" || s.owns(b, model) {
			emit(event)
		}
	})
}

// readEvents splits a server-sent event stream into events, each with the
// model its "data:" line names.
func readEvents(r io.Reader, handle func(event []byte, model string)) {
	rd := bufio.NewReader(r)
	var event bytes.Buffer
	model := ""
	for {
		line, err := rd.ReadBytes('\n')
		if len(line) > 0 {
			event.Write(line)
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) == 0 {
				handle(append([]byte(nil), event.Bytes()...), model)
				event.Reset()
				model = ""
			} else if m, ok := eventModel(trimmed); ok {
				model = m
			}
		}
		if err != nil {
			return
		}
	}
}

func eventModel(line []byte) (string, bool) {
	data, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return "", false
	}
	var e struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(bytes.TrimSpace(data), &e) != nil {
		return "", false
	}
	return e.Model, true
}
