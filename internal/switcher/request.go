package switcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxBody bounds the request bodies llamaenv reads to find the model. Chat
// requests with images are large, so the limit is generous.
const maxBody = 1 << 30

// requestModel reads the model of a request: the "model" query parameter or
// the "model" field of a JSON body. The body stays readable for the proxy.
func requestModel(r *http.Request) (string, error) {
	if m := r.URL.Query().Get("model"); m != "" {
		return m, nil
	}
	if !hasJSONBody(r) {
		return "", nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	_ = r.Body.Close()
	if err != nil {
		return "", fmt.Errorf("read request body: %w", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	var v struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &v) // not JSON: no model, the default router answers
	return v.Model, nil
}

// hasJSONBody says whether a request may carry a JSON body with a model.
func hasJSONBody(r *http.Request) bool {
	if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
		return false
	}
	ct := r.Header.Get("Content-Type")
	return ct == "" || strings.Contains(ct, "json")
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg, "type": "llamaenv"}})
}

// The helpers below talk to routers that llamaenv started on 127.0.0.1.

func (s *Switcher) getJSON(ctx context.Context, url string, v any) error {
	resp, err := s.do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return json.NewDecoder(resp.Body).Decode(v)
}

func (s *Switcher) get(ctx context.Context, url string) error {
	resp, err := s.do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (s *Switcher) post(ctx context.Context, url string, body []byte) error {
	resp, err := s.do(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (s *Switcher) do(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	resp, err := s.send(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s %s: HTTP %s", method, url, resp.Status)
	}
	return resp, nil
}

// send sends a request to a router and returns its answer, whatever the status.
func (s *Switcher) send(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	// G704: the URL is a local router that llamaenv started.
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body)) //nolint:gosec // local router URL, see above
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return s.client.Do(req) //nolint:gosec // local router URL, see above
}
