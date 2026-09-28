package switcher

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain lets the test binary act as a fake llama.cpp router:
// "<test binary> fake --name N --models a,b --host H --port P".
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "fake" {
		runFakeRouter(os.Args[2:])
		return
	}
	os.Exit(m.Run())
}

// fakeRouter serves the router endpoints that llamaenv and the Llama app use.
// Every answer names the router, so tests can see who served a request.
type fakeRouter struct {
	name    string
	mu      sync.Mutex
	order   []string
	status  map[string]string
	subs    []chan string
	reloads int
}

func runFakeRouter(args []string) {
	fs := flag.NewFlagSet("fake", flag.ExitOnError)
	name := fs.String("name", "", "")
	models := fs.String("models", "", "")
	host := fs.String("host", "127.0.0.1", "")
	port := fs.Int("port", 0, "")
	_ = fs.Parse(args)

	f := &fakeRouter{name: *name, status: map[string]string{}}
	for _, m := range strings.Split(*models, ",") {
		f.status[m] = "unloaded"
		f.order = append(f.order, m)
	}
	_, _ = fmt.Fprintln(os.Stderr, "router "+*name+" started")
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"status":"ok"}`) })
	mux.HandleFunc("/models", f.list)
	mux.HandleFunc("/v1/models", f.list)
	mux.HandleFunc("/models/load", f.change("loaded"))
	mux.HandleFunc("/models/unload", f.change("unloaded"))
	mux.HandleFunc("/models/sse", f.events)
	mux.HandleFunc("/v1/chat/completions", f.chat)
	srv := &http.Server{Addr: joinHostPort(*host, *port), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (f *fakeRouter) event(model string) string {
	return fmt.Sprintf(`data: {"model":%q,"event":"model_status","data":{"status":%q,"router":%q}}`+"\n\n", model, f.status[model], f.name)
}

// set changes a model's status and tells every event listener. Callers hold mu.
func (f *fakeRouter) set(model, status string) {
	f.status[model] = status
	for _, c := range f.subs {
		select {
		case c <- f.event(model):
		default:
		}
	}
}

func (f *fakeRouter) list(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Query().Get("reload") != "" {
		f.reloads++
	}
	data := []map[string]any{}
	for _, m := range f.order {
		data = append(data, map[string]any{"id": m, "owned_by": f.name, "status": map[string]string{"value": f.status[m]}})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data, "reloads": f.reloads})
}

func (f *fakeRouter) change(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.status[body.Model]; !ok {
			http.Error(w, "unknown model "+body.Model, http.StatusNotFound)
			return
		}
		f.set(body.Model, status)
		_, _ = io.WriteString(w, `{"success":true}`)
	}
}

func (f *fakeRouter) events(w http.ResponseWriter, r *http.Request) {
	c := make(chan string, 64)
	f.mu.Lock()
	f.subs = append(f.subs, c)
	for _, m := range f.order { // like a real router, report every model at the start
		c <- f.event(m)
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	for {
		select {
		case line := <-c:
			_, _ = io.WriteString(w, line)
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (f *fakeRouter) chat(w http.ResponseWriter, r *http.Request) {
	var body struct{ Model string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	_, known := f.status[body.Model]
	if known && f.status[body.Model] != "loaded" {
		f.set(body.Model, "loaded") // routers load a model on first use
	}
	f.mu.Unlock()
	if !known {
		http.Error(w, "unknown model", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, part := range []string{"served by ", f.name} {
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", part)
		w.(http.Flusher).Flush()
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

const (
	gemma  = "ggml-org/gemma-4-e4b-it-GGUF:Q4_0"
	bonsai = "prism-ml/Ternary-Bonsai-2-27B-gguf:PQ2_0"
)

type harness struct {
	t    *testing.T
	base string
	done chan error
}

func fake(name string) Launch {
	return Launch{Program: os.Args[0], Prefix: []string{"fake", "--name", name, "--models", gemma + "," + bonsai}}
}

// start runs a switcher with a fake official router and a fake "prism"
// runtime. Both routers see both models, as real routers over one shared
// Hugging Face cache do.
func start(t *testing.T, mutate func(*Options)) *harness {
	t.Helper()
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	opt := Options{
		Args:        ServeArgs{Host: "127.0.0.1", Port: port},
		Default:     fake("official"),
		DefaultName: "official",
		Runtimes:    map[string]Launch{"prism": fake("prism")},
		Owner: func(m string) string {
			if strings.HasPrefix(m, "prism-ml/") {
				return "prism"
			}
			return ""
		},
		Exclusive: true,
		StateFile: t.TempDir() + "/switcher.json",
	}
	if mutate != nil {
		mutate(&opt)
	}
	h := &harness{t: t, base: fmt.Sprintf("http://127.0.0.1:%d", port), done: make(chan error, 1)}
	// Cleanups run last-in, first-out: cancel, then wait for the switcher.
	t.Cleanup(func() {
		select {
		case <-h.done:
		case <-time.After(15 * time.Second):
			t.Error("switcher did not stop")
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { h.done <- Run(ctx, opt) }()
	h.waitUp()
	return h
}

func (h *harness) waitUp() {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if code, _ := h.request(http.MethodGet, "/health", ""); code == http.StatusOK {
			return
		}
		select {
		case err := <-h.done:
			h.t.Fatalf("switcher stopped during startup: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			h.t.Fatal("switcher did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// request sends a request to the switcher and returns the status and body;
// a connection error returns status 0.
func (h *harness) request(method, path, body string) (int, string) {
	req, err := http.NewRequestWithContext(context.Background(), method, h.base+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (h *harness) models(path string) map[string]map[string]any {
	h.t.Helper()
	_, body := h.request(http.MethodGet, path, "")
	var list struct{ Data []map[string]any }
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		h.t.Fatalf("%s: %v in %q", path, err, body)
	}
	out := map[string]map[string]any{}
	for _, m := range list.Data {
		id, _ := m["id"].(string)
		if _, dup := out[id]; dup {
			h.t.Errorf("%s is listed twice", id)
		}
		out[id] = m
	}
	return out
}

func (h *harness) status(model string) string {
	st, _ := h.models("/models")[model]["status"].(map[string]any)
	v, _ := st["value"].(string)
	return v
}

// chat returns the status and, on success, the streamed text.
func (h *harness) chat(model string) (int, string) {
	code, body := h.request(http.MethodPost, "/v1/chat/completions",
		fmt.Sprintf(`{"model":%q,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, model))
	if code != http.StatusOK {
		return code, body
	}
	var text strings.Builder
	for _, l := range strings.Split(body, "\n") {
		var chunk struct {
			Choices []struct{ Delta struct{ Content string } }
		}
		if d, ok := strings.CutPrefix(l, "data: "); ok && json.Unmarshal([]byte(d), &chunk) == nil && len(chunk.Choices) > 0 {
			text.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	return code, text.String()
}

func (h *harness) wantChat(model, want string) {
	h.t.Helper()
	if code, text := h.chat(model); code != http.StatusOK || text != want {
		h.t.Errorf("%s: %d %q, want %q", model, code, text, want)
	}
}

func (h *harness) wantStatus(model, want string) {
	h.t.Helper()
	if got := h.status(model); got != want {
		h.t.Errorf("%s is %s, want %s", model, got, want)
	}
}

func TestEachModelIsListedOnceByTheRouterThatOwnsIt(t *testing.T) {
	h := start(t, nil)
	for _, path := range []string{"/models", "/v1/models"} {
		m := h.models(path)
		if len(m) != 2 {
			t.Fatalf("%s: got %d models, want 2", path, len(m))
		}
		if got := m[gemma]["owned_by"]; got != "official" {
			t.Errorf("%s: gemma listed by %v, want official", path, got)
		}
		if got := m[bonsai]["owned_by"]; got != "prism" {
			t.Errorf("%s: bonsai listed by %v, want prism", path, got)
		}
	}
}

func TestRequestsGoToTheRuntimeThatOwnsTheModel(t *testing.T) {
	h := start(t, nil)
	h.wantChat(gemma, "served by official")
	h.wantChat(bonsai, "served by prism")
}

func TestSwitchingModelsUnloadsTheOtherRuntime(t *testing.T) {
	h := start(t, nil)
	h.chat(gemma)
	h.wantStatus(gemma, "loaded")
	h.chat(bonsai)
	h.wantStatus(gemma, "unloaded")
	h.wantStatus(bonsai, "loaded")
	// And back, through an explicit load as the Llama app does it.
	h.request(http.MethodPost, "/models/load", `{"model":"`+gemma+`"}`)
	h.wantStatus(bonsai, "unloaded")
	h.wantStatus(gemma, "loaded")
}

func TestNonExclusiveKeepsBothLoaded(t *testing.T) {
	h := start(t, func(o *Options) { o.Exclusive = false })
	h.chat(gemma)
	h.chat(bonsai)
	h.wantStatus(gemma, "loaded")
	h.wantStatus(bonsai, "loaded")
}

// readEventsFrom collects the events of the switcher's merged stream.
func (h *harness) readEventsFrom(ctx context.Context) <-chan map[string]any {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.base+"/models/sse", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:bodyclose // closed by the reader goroutine below
	if err != nil {
		h.t.Fatal(err)
	}
	events := make(chan map[string]any, 32)
	go func() {
		defer func() { _ = resp.Body.Close() }()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			var e map[string]any
			if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok && json.Unmarshal([]byte(d), &e) == nil {
				events <- e
			}
		}
	}()
	return events
}

func TestEventStreamsAreMergedByOwner(t *testing.T) {
	h := start(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events := h.readEventsFrom(ctx)
	h.chat(bonsai) // prism loads bonsai and reports it
	owner := map[string]string{gemma: "official", bonsai: "prism"}
	seen := map[string]bool{}
	for !seen[bonsai+" loaded"] {
		select {
		case e := <-events:
			model, _ := e["model"].(string)
			data, _ := e["data"].(map[string]any)
			if data["router"] != owner[model] {
				t.Fatalf("event for %s came from %v", model, data["router"])
			}
			seen[fmt.Sprintf("%s %v", model, data["status"])] = true
		case <-ctx.Done():
			t.Fatalf("no load event for bonsai; saw %v", seen)
		}
	}
}

func TestUnavailableRuntimeDoesNotAffectOtherModels(t *testing.T) {
	h := start(t, func(o *Options) {
		o.Runtimes = map[string]Launch{}
		o.Unavailable = map[string]error{"prism": errors.New("not installed")}
	})
	h.wantChat(gemma, "served by official")
	if code, body := h.chat(bonsai); code != http.StatusServiceUnavailable || !strings.Contains(body, "needs runtime prism") {
		t.Errorf("bonsai: %d %s", code, body)
	}
	// The model stays visible, listed by the default router.
	if got := h.models("/models")[bonsai]["owned_by"]; got != "official" {
		t.Errorf("bonsai listed by %v, want official", got)
	}
}

func TestBrokenRuntimeStillStartsTheSwitcher(t *testing.T) {
	h := start(t, func(o *Options) {
		o.Runtimes = map[string]Launch{"prism": {Program: "/definitely/missing/llama-server"}}
	})
	h.wantChat(gemma, "served by official")
	if code, body := h.chat(bonsai); code != http.StatusServiceUnavailable {
		t.Errorf("bonsai: %d %s", code, body)
	}
}

func TestMissingDefaultRouterIsASetupError(t *testing.T) {
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	err = Run(context.Background(), Options{
		Args:    ServeArgs{Host: "127.0.0.1", Port: port},
		Default: Launch{Program: "/definitely/missing/llama"},
	})
	if !errors.Is(err, ErrSetup) {
		t.Fatalf("got %v, want ErrSetup", err)
	}
}

func TestStateFileListsTheRouters(t *testing.T) {
	var state string
	start(t, func(o *Options) { state = o.StateFile })
	data, err := os.ReadFile(state) //nolint:gosec // a test's own temporary file
	if err != nil {
		t.Fatal(err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Backends) != 2 || st.Backends[0].Name != "official" || st.Backends[1].Name != "prism" {
		t.Errorf("state: %+v", st)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestReloadReachesEveryRouter(t *testing.T) {
	var state string
	h := start(t, func(o *Options) { state = o.StateFile })
	if code, _ := h.request(http.MethodGet, "/models?reload=1", ""); code != http.StatusOK {
		t.Fatalf("reload: %d", code)
	}
	data, err := os.ReadFile(state) //nolint:gosec // a test's own temporary file
	if err != nil {
		t.Fatal(err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	for _, b := range st.Backends {
		// Ask each fake router directly how many reloads it saw.
		direct := &harness{t: t, base: fmt.Sprintf("http://127.0.0.1:%d", b.Port)}
		if _, body := direct.request(http.MethodGet, "/models", ""); !strings.Contains(body, `"reloads":1`) {
			t.Errorf("router %s was not reloaded: %s", b.Name, body)
		}
	}
}

func TestRuntimeLogsAreMarked(t *testing.T) {
	logs := &lockedBuffer{}
	start(t, func(o *Options) { o.Stderr = logs })
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "[prism] router prism started") {
		if time.Now().After(deadline) {
			t.Fatalf("runtime log lines are not marked:\n%s", logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), "router official started") || strings.Contains(logs.String(), "[official]") {
		t.Errorf("default router output must stay unmarked:\n%s", logs.String())
	}
}
