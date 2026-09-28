package switcher

import (
	"context"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestParseServeArgsKeepsTheLlamaAppsArguments(t *testing.T) {
	a, err := ParseServeArgs([]string{"--port", "2276", "--jinja", "--sleep-idle-seconds", "300", "--models-preset", `C:\x.ini`})
	if err != nil {
		t.Fatal(err)
	}
	if a.Address() != "127.0.0.1:2276" {
		t.Errorf("address %s", a.Address())
	}
	want := []string{"--jinja", "--sleep-idle-seconds", "300", "--models-preset", `C:\x.ini`}
	if !reflect.DeepEqual(a.Rest, want) {
		t.Errorf("rest %v", a.Rest)
	}
	if got := a.ForBackend(9000); !reflect.DeepEqual(got, append(want, "--host", "127.0.0.1", "--port", "9000")) {
		t.Errorf("backend args %v", got)
	}
}

func TestParseServeArgsAddresses(t *testing.T) {
	cases := map[string][]string{
		"0.0.0.0:8081":   {"--host=0.0.0.0", "--port=8081"},
		"127.0.0.1:8080": nil,
		"[::1]:8080":     {"--host", "::1"},
	}
	for want, args := range cases {
		if a, err := ParseServeArgs(args); err != nil || a.Address() != want || len(a.Rest) != 0 {
			t.Errorf("%v: %s %v %v, want %s", args, a.Address(), a.Rest, err, want)
		}
	}
	for _, bad := range [][]string{{"--port"}, {"--port", "x"}, {"--port", "70000"}} {
		if _, err := ParseServeArgs(bad); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
}

func TestUsesModel(t *testing.T) {
	cases := map[string]bool{
		"POST /v1/chat/completions": true,
		"POST /models/load":         true,
		"POST /models/unload":       false,
		"POST /tokenize":            false,
		"GET /props":                false,
		"DELETE /models":            false,
	}
	for c, want := range cases {
		method, path, _ := strings.Cut(c, " ")
		r := httptest.NewRequestWithContext(context.Background(), method, path, nil)
		if got := usesModel(r); got != want {
			t.Errorf("%s: %v, want %v", c, got, want)
		}
	}
	if !isClosed(nil) {
		t.Error("nil channel counts as open")
	}
}

func TestRequestModel(t *testing.T) {
	cases := []struct {
		method, target, contentType, body, want string
	}{
		{"POST", "/v1/chat/completions", "application/json", `{"model":"a/b:Q4"}`, "a/b:Q4"},
		{"POST", "/v1/chat/completions", "", `{"model":"a/b:Q4"}`, "a/b:Q4"},
		{"POST", "/v1/audio", "multipart/form-data", `model=x`, ""},
		{"POST", "/completion", "application/json", `not json`, ""},
		{"GET", "/props?model=a/b:Q4", "", "", "a/b:Q4"},
		{"GET", "/props", "", "", ""},
	}
	for _, c := range cases {
		r := httptest.NewRequestWithContext(context.Background(), c.method, c.target, strings.NewReader(c.body))
		if c.contentType != "" {
			r.Header.Set("Content-Type", c.contentType)
		}
		got, err := requestModel(r)
		if err != nil || got != c.want {
			t.Errorf("%s %s %q: got %q %v, want %q", c.method, c.target, c.body, got, err, c.want)
		}
		if rest, _ := io.ReadAll(r.Body); c.method == "POST" && string(rest) != c.body {
			t.Errorf("%s: body not kept for the proxy: %q", c.target, rest)
		}
	}
}
