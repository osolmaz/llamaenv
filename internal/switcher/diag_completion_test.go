package switcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestCompletedRequestMustNotGainLateStall(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			once.Do(func() { close(entered) })
			<-release
		}
		if r.URL.Path == "/models" {
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer router.Close()
	defer onceRelease(release)()
	base, _ := url.Parse(router.URL)
	d := newDiag(Options{LogDir: t.TempDir(), StallAfter: 20 * time.Millisecond})
	defer d.close()
	s := &Switcher{diag: d, client: router.Client()}
	b := &backend{name: "test-runtime", base: base}
	tr := &tracked{}
	tr.last.Store(time.Now().Add(-time.Second).UnixNano())
	d.add(Event{Kind: "request", ID: 1, Model: "test-model", Runtime: b.name})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exited := make(chan struct{})
	go func() { defer close(exited); s.watchStall(ctx, 1, b, router.URL, "test-model", tr) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("stall probe never started")
	}
	// Finish the request while its health probe is still blocked.
	cancel()
	d.add(Event{Kind: "done", ID: 1, Model: "test-model", Runtime: b.name})
	close(release)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not stop")
	}
	wantNoStall(t, d.dir)
}

func onceRelease(ch chan struct{}) func() {
	return func() {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func TestCompletionCancelsInProgressProbe(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			close(entered)
			<-r.Context().Done()
			close(canceled)
		case "/v1/chat/completions":
			select {
			case <-entered:
				_, _ = w.Write([]byte("finished"))
			case <-time.After(2 * time.Second):
				http.Error(w, "probe never started", 500)
			}
		default:
			_, _ = w.Write([]byte("{}"))
		}
	}))
	defer router.Close()
	base, _ := url.Parse(router.URL)
	d := newDiag(Options{LogDir: t.TempDir(), StallAfter: 20 * time.Millisecond})
	defer d.close()
	s := &Switcher{diag: d, client: router.Client()}
	b := &backend{name: "test-runtime", base: base, proxy: httputil.NewSingleHostReverseProxy(base)}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://switcher/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() { defer close(finished); s.serveTracked(w, req, b, "test-model") }()
	wantSignal(t, finished, "completion waited for the probe timeout")
	wantSignal(t, canceled, "completion did not cancel the health probe")
	if w.Body.String() != "finished" {
		t.Fatalf("response: %q", w.Body.String())
	}
	wantNoStall(t, d.dir)
}

// A restart may hold b.mu for the full startup timeout. Once its proxy
// response ends, a request must not wait for that lock through its watcher.
func TestFinishedRequestMustNotWaitForBackendRestartLock(t *testing.T) {
	healthStarted := make(chan struct{})
	healthCanceled := make(chan struct{})
	finishChat := make(chan struct{})
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			close(healthStarted)
			<-r.Context().Done()
			close(healthCanceled)
		case "/v1/chat/completions":
			select {
			case <-finishChat:
				_, _ = w.Write([]byte("finished"))
			case <-r.Context().Done():
			}
		default:
			_, _ = w.Write([]byte("{}"))
		}
	}))
	defer router.Close()
	base, _ := url.Parse(router.URL)
	d := newDiag(Options{LogDir: t.TempDir(), StallAfter: 20 * time.Millisecond})
	defer d.close()
	s := &Switcher{diag: d, client: router.Client()}
	b := &backend{name: "test-runtime", base: base, proxy: httputil.NewSingleHostReverseProxy(base)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://switcher/v1/chat/completions", nil)
	finished := make(chan struct{})
	go func() { defer close(finished); s.serveTracked(httptest.NewRecorder(), r, b, "test-model") }()
	wantSignal(t, healthStarted, "health probe did not start")
	b.mu.Lock()
	defer func() {
		b.mu.Unlock()
		wantSignal(t, finished, "handler did not stop after the backend lock was released")
	}()
	close(finishChat)
	wantSignal(t, healthCanceled, "request completion did not cancel the probe")
	wantSignal(t, finished, "finished request is blocked by the backend restart lock")
	wantNoStall(t, d.dir)
}

func wantSignal(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func wantNoStall(t *testing.T, dir string) {
	t.Helper()
	if e, ok := findEvent(dir, func(e Event) bool { return e.Kind == "stall" }); ok {
		t.Fatalf("completed request produced a stall: %+v", e)
	}
}
