package switcher

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/osolmaz/llamaenv/internal/proc"
)

// ErrSetup means the switcher could not start its default router. The shim
// then runs the official "llama serve" directly: the standard path.
var ErrSetup = errors.New("switcher setup failed")

const startTimeout = 2 * time.Minute

// Options configure one switcher run.
type Options struct {
	Args ServeArgs
	// Default serves every model without a mapping.
	Default     Launch
	DefaultName string
	// Runtimes are the other routers, by runtime name.
	Runtimes map[string]Launch
	// Owner returns the runtime mapped to a model ID, or "".
	Owner func(modelID string) string
	// Unavailable explains mapped runtimes that could not be resolved.
	Unavailable map[string]error
	// Exclusive keeps at most one runtime with loaded models.
	Exclusive bool
	// StateFile is written while the switcher runs, for "llamaenv status".
	StateFile string
	Stdout    io.Writer
	Stderr    io.Writer
	Log       func(string)
}

// Switcher routes requests between routers.
type Switcher struct {
	opt      Options
	def      *backend
	runtimes map[string]*backend
	client   *http.Client

	activeMu sync.Mutex
	active   *backend // the backend that last got a model request
}

// Run serves until ctx ends or the default router stops.
func Run(ctx context.Context, opt Options) error {
	if opt.Log == nil {
		opt.Log = func(string) {}
	}
	group, err := proc.NewGroup()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSetup, err)
	}
	defer group.Stop(10 * time.Second)

	s := newSwitcher(opt, group)
	listener, err := s.startRouters(ctx)
	if err != nil {
		return err
	}
	s.writeState()
	defer func() { _ = os.Remove(opt.StateFile) }()
	return s.serve(ctx, listener)
}

func newSwitcher(opt Options, group *proc.Group) *Switcher {
	s := &Switcher{opt: opt, runtimes: map[string]*backend{}, client: &http.Client{Timeout: 30 * time.Second}}
	s.def = &backend{name: opt.DefaultName, launch: opt.Default, args: opt.Args, group: group, out: opt.Stdout, errOut: opt.Stderr}
	for name, l := range opt.Runtimes {
		pw := prefixWriter(opt.Stderr, "["+name+"] ")
		s.runtimes[name] = &backend{name: name, launch: l, args: opt.Args, group: group, out: pw, errOut: pw}
	}
	return s
}

// startRouters takes the client's address, then starts the default router,
// which must work, and the runtimes, which may fail on their own.
func (s *Switcher) startRouters(ctx context.Context) (net.Listener, error) {
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", s.opt.Args.Address())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSetup, err)
	}
	if err := s.def.start(ctx, startTimeout); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("%w: %w", ErrSetup, err)
	}
	var wg sync.WaitGroup
	for _, b := range s.runtimes {
		wg.Add(1)
		go func(b *backend) {
			defer wg.Done()
			if err := b.start(ctx, startTimeout); err != nil {
				s.opt.Log(err.Error())
			}
		}(b)
	}
	wg.Wait()
	return listener, nil
}

func (s *Switcher) serve(ctx context.Context, listener net.Listener) error {
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 30 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(listener) }()
	s.opt.Log("llamaenv listening on http://" + s.opt.Args.Address())

	var err error
	select {
	case <-ctx.Done():
	case <-s.def.done():
		err = errors.New("the default router stopped")
	case err = <-serveErr:
	}
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	return err
}

// ServeHTTP routes one request.
func (s *Switcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.serveSpecial(w, r) {
		return
	}
	model, err := requestModel(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	b, err := s.backendFor(r.Context(), model)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if model != "" && s.opt.Exclusive && usesModel(r) {
		s.makeActive(r.Context(), b)
	}
	b.serve(w, r)
}

// serveSpecial answers the requests that concern every router: the model
// list, the event stream, and a preset reload.
func (s *Switcher) serveSpecial(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	q := r.URL.Query()
	switch {
	case r.URL.Path == "/models/sse":
		s.serveEvents(w, r)
	case r.URL.Path == "/models" && q.Get("reload") != "":
		s.broadcast(w, r)
	case (r.URL.Path == "/models" || r.URL.Path == "/v1/models") && q.Get("model") == "":
		s.serveModelList(w, r)
	default:
		return false
	}
	return true
}

// backendFor returns the router that owns a model: its mapped runtime, or the
// default router.
func (s *Switcher) backendFor(ctx context.Context, model string) (*backend, error) {
	name := s.runtimeOf(model)
	if name == "" {
		return s.def, nil
	}
	if reason, ok := s.opt.Unavailable[name]; ok {
		return nil, fmt.Errorf("model %s needs runtime %s, which is not available: %w", model, name, reason)
	}
	b, ok := s.runtimes[name]
	if !ok {
		return nil, fmt.Errorf("model %s needs runtime %s, which is not configured", model, name)
	}
	if err := b.ensure(ctx); err != nil {
		return nil, fmt.Errorf("model %s needs runtime %s: %w", model, name, err)
	}
	return b, nil
}

// runtimeOf returns the runtime router for a model, or "" for the default
// router: also when the model is mapped to the runtime that is the default.
func (s *Switcher) runtimeOf(model string) string {
	if model == "" || s.opt.Owner == nil {
		return ""
	}
	if name := s.opt.Owner(model); name != s.opt.DefaultName {
		return name
	}
	return ""
}

// owns says whether a backend should list and report a model.
func (s *Switcher) owns(b *backend, model string) bool {
	name := s.runtimeOf(model)
	if name == "" {
		return b == s.def
	}
	if rb, ok := s.runtimes[name]; ok && rb.available() {
		return b == rb
	}
	// The runtime is down: the default router keeps the model visible, and a
	// load returns a clear error from backendFor.
	return b == s.def
}

// usesModel says whether a request loads or runs a model.
func usesModel(r *http.Request) bool {
	switch r.URL.Path {
	case "/models/unload", "/props", "/slots", "/metrics", "/health":
		return false
	}
	return r.Method == http.MethodPost && !strings.HasPrefix(r.URL.Path, "/tokenize") && !strings.HasPrefix(r.URL.Path, "/detokenize")
}

// makeActive unloads the models of every other backend before a request that
// needs b, so two runtimes never hold models in memory at the same time.
func (s *Switcher) makeActive(ctx context.Context, b *backend) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.active == b && b.available() {
		return
	}
	for _, o := range s.all() {
		if o != b && o.available() {
			s.unloadAll(ctx, o, b.name)
		}
	}
	s.active = b
}

func (s *Switcher) unloadAll(ctx context.Context, o *backend, reason string) {
	for _, m := range s.loadedModels(ctx, o) {
		s.opt.Log(fmt.Sprintf("unloading %s from %s before a request for %s", m, o.name, reason))
		body, err := json.Marshal(map[string]string{"model": m})
		if err != nil {
			continue
		}
		if err := s.post(ctx, o.url("/models/unload"), body); err != nil {
			s.opt.Log("unload failed: " + err.Error())
		}
	}
}

// all returns the default backend first, then the runtimes by name.
func (s *Switcher) all() []*backend {
	names := make([]string, 0, len(s.runtimes))
	for n := range s.runtimes {
		names = append(names, n)
	}
	sort.Strings(names)
	out := []*backend{s.def}
	for _, n := range names {
		out = append(out, s.runtimes[n])
	}
	return out
}

// State is what "llamaenv status" reads.
type State struct {
	PID      int            `json:"pid"`
	Address  string         `json:"address"`
	Backends []BackendState `json:"backends"`
}

// BackendState is one router in the state file.
type BackendState struct {
	Name    string `json:"name"`
	Program string `json:"program"`
	Port    int    `json:"port"`
	Error   string `json:"error,omitempty"`
}

func (s *Switcher) writeState() {
	if s.opt.StateFile == "" {
		return
	}
	if err := saveJSON(s.opt.StateFile, s.state()); err != nil {
		s.opt.Log("state file: " + err.Error())
	}
}

func (s *Switcher) state() State {
	st := State{PID: os.Getpid(), Address: s.opt.Args.Address()}
	for _, b := range s.all() {
		port, err := b.status()
		bs := BackendState{Name: b.name, Program: b.launch.Program, Port: port}
		if err != nil {
			bs.Error = err.Error()
		}
		st.Backends = append(st.Backends, bs)
	}
	for name, reason := range s.opt.Unavailable {
		st.Backends = append(st.Backends, BackendState{Name: name, Error: reason.Error()})
	}
	return st
}

func saveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// prefixWriter marks each output line of a runtime router with its name.
func prefixWriter(w io.Writer, prefix string) io.Writer {
	if w == nil {
		return io.Discard
	}
	pr, pw := io.Pipe()
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			_, _ = fmt.Fprintln(w, prefix+sc.Text())
		}
	}()
	return pw
}
