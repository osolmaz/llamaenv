package switcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"sync"
	"time"

	"github.com/osolmaz/llamaenv/internal/proc"
)

// Launch is how to start a router: a program and its leading arguments, for
// example the unified llama program with "serve".
type Launch struct {
	Program string
	Prefix  []string
}

// backend is one llama.cpp router on a private local port.
type backend struct {
	name   string
	launch Launch
	args   ServeArgs
	group  *proc.Group
	out    io.Writer
	errOut io.Writer

	mu      sync.Mutex
	port    int
	base    *url.URL
	proxy   *httputil.ReverseProxy
	cmd     *exec.Cmd
	exited  chan struct{}
	err     error     // why the backend is not available
	started time.Time // last start attempt
}

// start starts the router and waits until it answers /health.
func (b *backend) start(ctx context.Context, timeout time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.startLocked(ctx, timeout)
}

func (b *backend) startLocked(ctx context.Context, timeout time.Duration) error {
	b.started = time.Now()
	port, err := freePort()
	if err != nil {
		b.err = err
		return err
	}
	args := append(append([]string(nil), b.launch.Prefix...), b.args.ForBackend(port)...)
	cmd := exec.CommandContext(context.WithoutCancel(ctx), b.launch.Program, args...) //nolint:gosec // G204: runs the llama program the user configured as a runtime
	cmd.Stdout, cmd.Stderr = b.out, b.errOut
	if err := b.group.Start(cmd); err != nil {
		b.err = fmt.Errorf("start %s: %w", b.launch.Program, err)
		return b.err
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	base := &url.URL{Scheme: "http", Host: joinHostPort("127.0.0.1", port)}
	proxy := httputil.NewSingleHostReverseProxy(base)
	proxy.FlushInterval = -1 // stream every chunk at once
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("runtime %s did not answer: %v", b.name, err))
	}
	b.port, b.base, b.proxy, b.cmd, b.exited, b.err = port, base, proxy, cmd, exited, nil
	if err := waitHealthy(ctx, base, exited, timeout); err != nil {
		b.err = fmt.Errorf("runtime %s: %w", b.name, err)
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return b.err
	}
	return nil
}

// ensure restarts a stopped backend on demand, at most once every 10 seconds.
func (b *backend) ensure(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cmd != nil && !isClosed(b.exited) && b.err == nil {
		return nil
	}
	if time.Since(b.started) < 10*time.Second {
		if b.err == nil {
			b.err = fmt.Errorf("runtime %s stopped", b.name)
		}
		return b.err
	}
	return b.startLocked(ctx, 2*time.Minute)
}

// available reports whether the router runs and answered its health check.
func (b *backend) available() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cmd != nil && b.err == nil && !isClosed(b.exited)
}

func (b *backend) status() (port int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	err = b.err
	if err == nil && (b.cmd == nil || isClosed(b.exited)) {
		err = errors.New("not running")
	}
	return b.port, err
}

func (b *backend) url(path string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.base.String() + path
}

func (b *backend) serve(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	p := b.proxy
	b.mu.Unlock()
	p.ServeHTTP(w, r)
}

func (b *backend) done() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exited
}

func waitHealthy(ctx context.Context, base *url.URL, exited <-chan struct{}, timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		if healthy(ctx, base) {
			return nil
		}
		select {
		case <-exited:
			return errors.New("exited during startup")
		case <-deadline:
			return fmt.Errorf("no answer on /health within %s", timeout)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func healthy(ctx context.Context, base *url.URL) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String()+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func freePort() (int, error) {
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	return port, l.Close()
}

func isClosed(c <-chan struct{}) bool {
	if c == nil {
		return true
	}
	select {
	case <-c:
		return true
	default:
		return false
	}
}
