package switcher

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/osolmaz/llamaenv/internal/logs"
)

// The disk budget of one switcher: each file and its ".1" at most maxLogFile.
const (
	maxLogFile   = 8 << 20
	keepDumps    = 3
	queueDepth   = 1024
	defaultStall = 60 * time.Second
	probeTimeout = 3 * time.Second
	maxProbeBody = 2 << 10
	maxField     = 512 // a client's model name or path, in an event
)

// EventsFile is the event log in a switcher's log folder.
const EventsFile = "events.jsonl"

// Event is one line of the event log.
type Event struct {
	Time    time.Time `json:"time"`
	PID     int       `json:"pid"`
	Kind    string    `json:"kind"` // start, log, exit, request, done, stall
	ID      uint64    `json:"id,omitempty"`
	Runtime string    `json:"runtime,omitempty"`
	Model   string    `json:"model,omitempty"`
	Path    string    `json:"path,omitempty"`
	Message string    `json:"message,omitempty"`
	// done
	Status      int   `json:"status,omitempty"`
	Bytes       int64 `json:"bytes,omitempty"`
	FirstByteMS int64 `json:"first_byte_ms,omitempty"`
	DurationMS  int64 `json:"duration_ms,omitempty"`
	ClientGone  bool  `json:"client_gone,omitempty"`
	// stall
	Probes map[string]string `json:"probes,omitempty"`
	Dump   string            `json:"dump,omitempty"`
	// log: lines dropped so far because stderr did not keep up
	Dropped int64 `json:"dropped,omitempty"`
}

// diag writes a switcher's diagnostics. Without a log folder it writes no
// files, and still never blocks on stderr.
type diag struct {
	dir     string
	events  *logs.File
	files   []*logs.File
	stderr  *logs.Queue // runtime output, to the switcher's stderr
	log     *logs.Queue // the switcher's own messages, to the caller's log
	stall   time.Duration
	nextID  atomic.Uint64
	pid     int
	dropped atomic.Int64 // the drop count that the last event reported
}

func newDiag(opt Options) *diag {
	d := &diag{dir: opt.LogDir, stall: opt.StallAfter, pid: os.Getpid()}
	if d.stall <= 0 {
		d.stall = defaultStall
	}
	stderr := opt.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	d.stderr = logs.NewQueue(func(line string) { _, _ = io.WriteString(stderr, line+"\n") }, queueDepth)
	say := opt.Log
	if say == nil {
		say = func(string) {}
	}
	d.log = logs.NewQueue(say, queueDepth)
	if d.dir != "" {
		d.events = d.open(EventsFile)
	}
	return d
}

// open opens a log file in the folder. A file that cannot be opened is left
// out: diagnostics must never stop the switcher.
func (d *diag) open(name string) *logs.File {
	if d.dir == "" {
		return nil
	}
	f, err := logs.Open(filepath.Join(d.dir, name), maxLogFile)
	if err != nil {
		d.stderr.Add("llamaenv: log file: " + err.Error())
		return nil
	}
	d.files = append(d.files, f)
	return f
}

func (d *diag) close() {
	d.log.Close(time.Second)
	d.stderr.Close(time.Second)
	for _, f := range d.files {
		_ = f.Close()
	}
}

func (d *diag) add(e Event) {
	e.Time, e.PID = time.Now().UTC(), d.pid
	// The client names the model, so it bounds no event by itself.
	e.Model, e.Path, e.Message = cut(e.Model), cut(e.Path), cut(e.Message)
	if n := d.stderr.Dropped() + d.log.Dropped(); n != d.dropped.Swap(n) {
		e.Dropped = n
	}
	line, err := json.Marshal(e)
	if err == nil {
		_, _ = d.events.Write(append(line, '\n'))
	}
}

func cut(s string) string {
	if len(s) <= maxField {
		return s
	}
	return strings.ToValidUTF8(s[:maxField], "") + "..."
}

// say records one of the switcher's own messages and passes it to the
// caller's log, without waiting for it.
func (d *diag) say(msg string) {
	d.add(Event{Kind: "log", Message: msg})
	d.log.Add(msg)
}

// runtimeOutput is where a runtime router's output goes: its log file, and
// stderr with each line marked with the runtime's name.
func (d *diag) runtimeOutput(name string) *logs.Lines {
	f := d.open(name + ".log")
	prefix := "[" + name + "] "
	return logs.NewLines(func(line string) {
		_, _ = f.Write([]byte(line + "\n"))
		d.stderr.Add(prefix + line)
	})
}

// tracked counts what a model request sends back, and when.
type tracked struct {
	http.ResponseWriter
	status int
	bytes  atomic.Int64
	first  atomic.Int64 // UnixNano of the first body byte
	last   atomic.Int64 // UnixNano of the request start or the last write
}

func (t *tracked) WriteHeader(code int) {
	if t.status == 0 {
		t.status = code
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *tracked) Write(p []byte) (int, error) {
	if t.status == 0 {
		t.status = http.StatusOK
	}
	now := time.Now().UnixNano()
	t.first.CompareAndSwap(0, now)
	t.last.Store(now)
	n, err := t.ResponseWriter.Write(p)
	t.bytes.Add(int64(n))
	return n, err
}

// Flush and Unwrap keep streaming working through the wrapper.
func (t *tracked) Flush() { _ = http.NewResponseController(t.ResponseWriter).Flush() }

func (t *tracked) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// serveTracked serves a model request through b and records it: its start,
// its end, and a snapshot of b when no bytes come back for a while.
func (s *Switcher) serveTracked(w http.ResponseWriter, r *http.Request, b *backend, model string) {
	d := s.diag
	id := d.nextID.Add(1)
	start := time.Now()
	t := &tracked{ResponseWriter: w}
	t.last.Store(start.UnixNano())
	d.add(Event{Kind: "request", ID: id, Runtime: b.name, Model: model, Path: r.URL.Path})

	done := make(chan struct{})
	go s.watchStall(id, b, model, t, done)
	b.serve(t, r)
	close(done)

	e := Event{Kind: "done", ID: id, Runtime: b.name, Model: model, Path: r.URL.Path,
		Status: t.status, Bytes: t.bytes.Load(), DurationMS: time.Since(start).Milliseconds(),
		ClientGone: r.Context().Err() != nil}
	if first := t.first.Load(); first != 0 {
		e.FirstByteMS = time.Duration(first - start.UnixNano()).Milliseconds()
	}
	d.add(e)
}

// watchStall records one stall snapshot when the request goes silent.
func (s *Switcher) watchStall(id uint64, b *backend, model string, t *tracked, done <-chan struct{}) {
	tick := time.NewTicker(min(s.diag.stall/4, 5*time.Second))
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
		}
		silent := time.Since(time.Unix(0, t.last.Load()))
		if silent < s.diag.stall {
			continue
		}
		s.diag.add(Event{Kind: "stall", ID: id, Runtime: b.name, Model: model,
			Message: fmt.Sprintf("no bytes for %s", silent.Round(100*time.Millisecond)),
			Probes:  s.probe(b, model), Dump: s.diag.dump(id)})
		return
	}
}

// probe asks a router about a model, each question with a short timeout, so
// that a wedged router shows up as timeouts rather than as silence.
func (s *Switcher) probe(b *backend, model string) map[string]string {
	out := map[string]string{"health": s.probeGet(b.url("/health"))}
	var list struct {
		Data []struct {
			ID     string `json:"id"`
			Status struct {
				Value string   `json:"value"`
				Args  []string `json:"args"`
			} `json:"status"`
		} `json:"data"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	if err := s.getJSON(ctx, b.url("/models"), &list); err != nil {
		out["models"] = err.Error()
	}
	for _, m := range list.Data {
		if m.ID != model {
			continue
		}
		out["status"] = m.Status.Value
		if port := argValue(m.Status.Args, "--port"); port != "" {
			out["child_port"] = port + " " + portState(port)
		}
	}
	out["slots"] = s.probeGet(b.url("/slots?model=" + model))
	return out
}

func (s *Switcher) probeGet(url string) string {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	resp, err := s.send(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
	if err != nil {
		return resp.Status + ": " + err.Error()
	}
	return resp.Status + " " + string(body)
}

func portState(port string) string {
	c, err := (&net.Dialer{Timeout: probeTimeout}).Dial("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return "closed"
	}
	_ = c.Close()
	return "open"
}

func argValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// dump writes every goroutine's stack to the log folder and keeps the newest
// few dumps. It returns the file name, or "" without a folder.
func (d *diag) dump(id uint64) string {
	if d.dir == "" {
		return ""
	}
	name := fmt.Sprintf("stall-%s-%d.txt", time.Now().UTC().Format("20060102T150405"), id)
	f, err := os.Create(filepath.Join(d.dir, name)) //nolint:gosec // G304: a file in llamaenv's own log folder
	if err != nil {
		return ""
	}
	_ = pprof.Lookup("goroutine").WriteTo(f, 2)
	_ = f.Close()
	old, _ := filepath.Glob(filepath.Join(d.dir, "stall-*.txt"))
	sort.Strings(old)
	for len(old) > keepDumps {
		_ = os.Remove(old[0])
		old = old[1:]
	}
	return name
}

// InFlight is a model request that has started and not ended.
type InFlight struct {
	ID      uint64
	Runtime string
	Model   string
	Since   time.Time
	Stalled bool
}

// ReadInFlight returns the requests in flight of the switcher with this PID,
// from its event log, oldest first.
func ReadInFlight(dir string, pid int) []InFlight {
	open := map[uint64]*InFlight{}
	// A long request can start before the event log moved to ".1".
	path := filepath.Join(dir, EventsFile)
	scan := func(path string) { scanEvents(path, func(e Event) { track(open, e, pid) }) }
	scan(path + ".1")
	scan(path)
	out := make([]InFlight, 0, len(open))
	for _, r := range open {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// track applies one event of the switcher with this PID to its open requests.
func track(open map[uint64]*InFlight, e Event, pid int) {
	if e.PID != pid {
		return
	}
	switch e.Kind {
	case "request":
		open[e.ID] = &InFlight{ID: e.ID, Runtime: e.Runtime, Model: e.Model, Since: e.Time}
	case "stall":
		if r := open[e.ID]; r != nil {
			r.Stalled = true
		}
	case "done":
		delete(open, e.ID)
	}
}

// scanEvents calls fn for each readable event in an event log.
func scanEvents(path string, fn func(Event)) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			fn(e)
		}
	}
}

// LogDir is a switcher's log folder, under llamaenv's logs folder.
func LogDir(logs string, port int) string { return filepath.Join(logs, strconv.Itoa(port)) }
