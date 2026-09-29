package switcher

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// stuckWriter blocks every write until release closes, like a pipe that
// nobody reads.
type stuckWriter struct{ release chan struct{} }

func (w stuckWriter) Write(p []byte) (int, error) {
	<-w.release
	return len(p), nil
}

func chatty(name string, bytes int) Launch {
	l := fake(name)
	l.Prefix = append(l.Prefix, "--chatty", fmt.Sprint(bytes))
	return l
}

func eventLog(dir string) []Event {
	var out []Event
	scanEvents(filepath.Join(dir, EventsFile), func(e Event) { out = append(out, e) })
	return out
}

// findEvent returns the first event that matches.
func findEvent(dir string, match func(Event) bool) (Event, bool) {
	for _, e := range eventLog(dir) {
		if match(e) {
			return e, true
		}
	}
	return Event{}, false
}

// waitFor polls until ok is true.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A llama.cpp router blocks when its output stops draining. The switcher
// must keep draining it when its own stderr does not.
func TestRuntimeOutputNeverWaitsForStderr(t *testing.T) {
	stuck := stuckWriter{release: make(chan struct{})}
	t.Cleanup(func() { close(stuck.release) })
	dir := t.TempDir()
	h := start(t, func(o *Options) {
		o.Stderr = stuck
		o.LogDir = dir
		o.Runtimes["prism"] = chatty("prism", 1<<20)
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 3 {
			h.wantChat(bonsai, "served by prism")
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("requests hung behind a stderr that nobody reads")
	}
	if info, err := os.Stat(filepath.Join(dir, "prism.log")); err != nil || info.Size() < 2<<20 {
		t.Errorf("prism.log: %v, %v", info, err)
	}
	// The next event reports the lines that stderr lost.
	h.wantChat(gemma, "served by official")
	if _, ok := findEvent(dir, func(e Event) bool { return e.Dropped > 0 }); !ok {
		t.Error("no event reports the dropped lines")
	}
}

func TestModelRequestsAreRecorded(t *testing.T) {
	dir := t.TempDir()
	h := start(t, func(o *Options) { o.LogDir = dir })
	h.wantChat(bonsai, "served by prism")
	h.wantChat(gemma, "served by official")
	h.models("/models") // lists are not model requests
	if e, ok := findEvent(dir, func(e Event) bool { return e.Kind == "request" && e.Path != "/v1/chat/completions" }); ok {
		t.Errorf("recorded %s", e.Path)
	}
	for model, runtime := range map[string]string{bonsai: "prism", gemma: "official"} {
		e, _ := findEvent(dir, func(e Event) bool { return e.Kind == "done" && e.Model == model })
		want := Event{Kind: "done", ID: e.ID, PID: os.Getpid(), Runtime: runtime, Model: model, Path: "/v1/chat/completions", Status: http.StatusOK,
			Time: e.Time, Bytes: e.Bytes, FirstByteMS: e.FirstByteMS, DurationMS: e.DurationMS}
		if e.Bytes == 0 || !reflect.DeepEqual(e, want) {
			t.Errorf("%s: %+v", model, e)
		}
	}
	if r := ReadInFlight(dir, os.Getpid()); len(r) != 0 {
		t.Errorf("in flight: %+v", r)
	}
}

// hold sends a model request that the router answers only when the client
// goes away.
func hold(t *testing.T, h *harness, ctx context.Context) {
	t.Helper()
	body := fmt.Sprintf(`{"model":%q,"hold":true,"stream":true}`, bonsai)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.base+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	go func() {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
}

func checkStall(t *testing.T, dir string, stall Event) {
	t.Helper()
	if stall.Runtime != "prism" || !strings.HasPrefix(stall.Probes["health"], "200") || stall.Probes["status"] != "loaded" {
		t.Errorf("stall: %+v", stall)
	}
	if _, err := os.Stat(filepath.Join(dir, stall.Dump)); stall.Dump == "" || err != nil {
		t.Errorf("dump %q: %v", stall.Dump, err)
	}
	if r := ReadInFlight(dir, os.Getpid()); len(r) != 1 || !r[0].Stalled || r[0].Model != bonsai {
		t.Errorf("in flight: %+v", r)
	}
}

// A request that gets no bytes leaves a snapshot of its router, and a client
// that goes away cancels the request at the router.
func TestSilentRequestLeavesASnapshotAndAbortReachesTheRouter(t *testing.T) {
	dir := t.TempDir()
	h := start(t, func(o *Options) {
		o.LogDir = dir
		o.StallAfter = 200 * time.Millisecond
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hold(t, h, ctx)

	var stall Event
	waitFor(t, "a stall event", func() bool {
		var ok bool
		stall, ok = findEvent(dir, func(e Event) bool { return e.Kind == "stall" })
		return ok
	})
	checkStall(t, dir, stall)

	cancel()
	waitFor(t, "the router to see the client go away", func() bool {
		data, _ := os.ReadFile(filepath.Clean(filepath.Join(dir, "prism.log")))
		return strings.Contains(string(data), "client went away")
	})
	waitFor(t, "the done event", func() bool {
		_, ok := findEvent(dir, func(e Event) bool { return e.Kind == "done" && e.ID == stall.ID && e.ClientGone })
		return ok
	})
}

func TestEventsStayBoundedAndInFlightSpansRotation(t *testing.T) {
	dir := t.TempDir()
	d := newDiag(Options{LogDir: dir})
	d.add(Event{Kind: "request", ID: 1, Model: strings.Repeat("m", 1<<20)})
	d.close()
	// The event log moves to ".1" while the request runs.
	d = newDiag(Options{LogDir: dir})
	defer d.close()
	d.add(Event{Kind: "stall", ID: 1})
	info, err := os.Stat(filepath.Join(dir, EventsFile+".1"))
	if err != nil || info.Size() > 2<<10 {
		t.Errorf("one event takes %v bytes: %v", info.Size(), err)
	}
	r := ReadInFlight(dir, os.Getpid())
	if len(r) != 1 || !r[0].Stalled || len(r[0].Model) > maxField+3 {
		t.Errorf("in flight: %d requests", len(r))
	}
}
