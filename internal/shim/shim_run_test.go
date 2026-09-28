package shim

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/osolmaz/llamaenv/internal/runtimes"
)

// TestMain lets the test binary act as a fake official llama when
// FAKE_LLAMA is set: it prints its arguments to FAKE_LLAMA_OUT and exits with
// FAKE_LLAMA_EXIT. "serve" exits at once, like a llama that cannot start.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_LLAMA") == "1" {
		if out := os.Getenv("FAKE_LLAMA_OUT"); out != "" {
			if f, err := os.OpenFile(filepath.Clean(out), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				_, _ = fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
				_ = f.Close()
			}
		}
		code, _ := strconv.Atoi(os.Getenv("FAKE_LLAMA_EXIT"))
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// fakeLlama makes the test binary the official llama and returns the file
// its calls go to.
func fakeLlama(t *testing.T, exit int) string {
	t.Helper()
	out, _ := fakeLlamaHome(t, exit)
	return out
}

// fakeLlamaHome is fakeLlama that also returns llamaenv's home folder.
func fakeLlamaHome(t *testing.T, exit int) (string, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "calls.txt")
	home := t.TempDir()
	t.Setenv("FAKE_LLAMA", "1")
	t.Setenv("FAKE_LLAMA_OUT", out)
	t.Setenv("FAKE_LLAMA_EXIT", strconv.Itoa(exit))
	t.Setenv("LLAMAENV_OFFICIAL", os.Args[0])
	t.Setenv("LLAMAENV_HOME", home)
	return out, home
}

func calls(t *testing.T, path string) []string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Clean(path))
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestOtherCommandsPassThroughWithTheirExitCode(t *testing.T) {
	out := fakeLlama(t, 3)
	if code := Main([]string{"cli", "--list-devices"}); code != 3 {
		t.Errorf("exit code %d, want 3", code)
	}
	if got := calls(t, out); len(got) != 1 || got[0] != "cli --list-devices" {
		t.Errorf("calls %v", got)
	}
}

func TestServeWithoutMappingsIsThePlainOfficialServe(t *testing.T) {
	out := fakeLlama(t, 0)
	if code := Main([]string{"serve", "--port", "2276", "--jinja"}); code != 0 {
		t.Errorf("exit code %d", code)
	}
	if got := calls(t, out); len(got) != 1 || got[0] != "serve --port 2276 --jinja" {
		t.Errorf("calls %v", got)
	}
}

// With a mapping, the switcher starts the official router behind it. When
// that fails, the shim runs the official "llama serve" with the original
// arguments: the standard path.
func TestServeFallsBackToTheOfficialServeWhenTheSwitcherFails(t *testing.T) {
	out, home := fakeLlamaHome(t, 0)
	if err := os.WriteFile(filepath.Join(home, "runtimes.ini"), []byte("[runtime prism]\nmodels = prism-ml/Ternary-Bonsai-2-27B-gguf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	must(t, l.Close())
	if code := Main([]string{"serve", "--port", port, "--jinja"}); code != 0 {
		t.Errorf("exit code %d", code)
	}
	got := calls(t, out)
	if len(got) < 2 || got[len(got)-1] != "serve --port "+port+" --jinja" {
		t.Errorf("calls %v: want the switcher's try, then the plain serve", got)
	}
}

func TestBrokenConfigFallsBackToTheOfficialServe(t *testing.T) {
	out, home := fakeLlamaHome(t, 0)
	if err := os.WriteFile(filepath.Join(home, "runtimes.ini"), []byte("not ini\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := Main([]string{"serve", "--port", "2276"}); code != 0 {
		t.Errorf("exit code %d", code)
	}
	if got := calls(t, out); len(got) != 1 || got[0] != "serve --port 2276" {
		t.Errorf("calls %v", got)
	}
}

func TestDefaultRuntimeWithoutUnifiedLlamaIsAConfigError(t *testing.T) {
	_, home := fakeLlamaHome(t, 0)
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, runtimes.Exe("llama-server")), []byte("x"), 0o600))
	must(t, os.WriteFile(filepath.Join(home, "runtimes.ini"), []byte("[runtime prism]\npath = "+dir+"\n"), 0o600))
	must(t, os.WriteFile(filepath.Join(home, "llamaenv.ini"), []byte("default = prism\n"), 0o600))
	if _, _, err := options(os.Args[0], nil, func(string) {}); err == nil || !strings.Contains(err.Error(), "cannot serve every model") {
		t.Errorf("got %v", err)
	}
	must(t, os.WriteFile(filepath.Join(home, "llamaenv.ini"), []byte("default = missing\n"), 0o600))
	if _, _, err := options(os.Args[0], nil, func(string) {}); err == nil {
		t.Error("unknown default runtime accepted")
	}
}

func TestMissingOfficialLlamaIsReported(t *testing.T) {
	t.Setenv("LLAMAENV_OFFICIAL", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if code := Main([]string{"--version"}); code != 127 {
		t.Errorf("exit code %d, want 127", code)
	}
	if _, err := RealLlama(); err == nil {
		t.Error("found a llama that does not exist")
	}
}
