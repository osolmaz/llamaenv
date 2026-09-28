package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/osolmaz/llamaenv/internal/runtimes"
	"github.com/osolmaz/llamaenv/internal/switcher"
)

// TestMain lets the test binary act as a llama program that prints a
// version, for "llamaenv versions".
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_LLAMA") == "1" {
		_, _ = os.Stdout.WriteString("version: 9 (build 11200, fake)\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type env struct {
	t    *testing.T
	home string
}

func setup(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LLAMAENV_HOME", home)
	t.Setenv("LLAMAENV_OFFICIAL", os.Args[0])
	t.Setenv("FAKE_LLAMA", "1")
	return &env{t: t, home: home}
}

func (e *env) run(args ...string) (int, string) {
	var out, errOut bytes.Buffer
	code := Main(args, &out, &errOut)
	return code, out.String() + errOut.String()
}

func (e *env) ok(args ...string) string {
	e.t.Helper()
	code, out := e.run(args...)
	if code != 0 {
		e.t.Fatalf("llamaenv %s: exit %d\n%s", strings.Join(args, " "), code, out)
	}
	return out
}

func (e *env) fails(want string, args ...string) {
	e.t.Helper()
	code, out := e.run(args...)
	if code == 0 || !strings.Contains(out, want) {
		e.t.Errorf("llamaenv %s: exit %d, want an error containing %q:\n%s", strings.Join(args, " "), code, want, out)
	}
}

// runtimeDir creates a runtime folder with the given programs.
func (e *env) runtimeDir(programs ...string) string {
	dir := e.t.TempDir()
	for _, p := range programs {
		if err := os.WriteFile(filepath.Join(dir, runtimes.Exe(p)), []byte("x"), 0o600); err != nil {
			e.t.Fatal(err)
		}
	}
	return dir
}

func TestHelpAndVersion(t *testing.T) {
	e := setup(t)
	if out := e.ok("help"); !strings.Contains(out, "Work in progress") {
		t.Errorf("help: %s", out)
	}
	if out := e.ok(); !strings.Contains(out, "Usage") {
		t.Errorf("no args: %s", out)
	}
	if out := e.ok("version"); !strings.Contains(out, "llamaenv") {
		t.Errorf("version: %s", out)
	}
	e.fails("unknown command", "frobnicate")
}

func TestMapAModelToARuntimeAndBack(t *testing.T) {
	e := setup(t)
	model := "prism-ml/Ternary-Bonsai-2-27B-gguf"
	e.fails("unknown runtime", "map", model, "prism")
	e.ok("runtime", "add", "prism", e.runtimeDir("llama-server"))
	if out := e.ok("map", model, "prism"); !strings.Contains(out, "now runs on prism") {
		t.Errorf("map: %s", out)
	}
	out := e.ok("list")
	if !strings.Contains(out, model) || !strings.Contains(out, "ready") || !strings.Contains(out, "default runtime: official") {
		t.Errorf("list: %s", out)
	}
	if out := e.ok("runtime", "list"); !strings.Contains(out, "prism") || !strings.Contains(out, runtimes.Exe("llama-server")) {
		t.Errorf("runtime list: %s", out)
	}
	if out := e.ok("versions"); strings.Count(out, "build 11200") != 1 || !strings.Contains(out, "prism") {
		t.Errorf("versions: %s", out)
	}
	e.fails("unmap it first", "runtime", "remove", "prism")
	e.ok("unmap", model)
	e.fails("has no mapping", "unmap", model)
	if out := e.ok("list"); !strings.Contains(out, "no model mappings") {
		t.Errorf("list after unmap: %s", out)
	}
	e.ok("runtime", "remove", "prism")
	e.fails("unknown runtime", "runtime", "remove", "prism")
}

func TestUseSetsTheDefaultRuntime(t *testing.T) {
	e := setup(t)
	e.ok("runtime", "add", "prism", e.runtimeDir("llama-server"))
	e.fails("cannot serve every model", "use", "prism")
	e.ok("runtime", "add", "official-b1", e.runtimeDir("llama"))
	if out := e.ok("use", "official-b1"); !strings.Contains(out, "default runtime: official-b1") {
		t.Errorf("use: %s", out)
	}
	e.fails("is the default runtime", "runtime", "remove", "official-b1")
	e.ok("use", "official")
	e.ok("runtime", "remove", "official-b1")
	e.fails("unknown runtime", "use", "nope")
}

func TestUsageErrors(t *testing.T) {
	e := setup(t)
	e.fails("usage", "map", "only-one")
	e.fails("usage", "unmap")
	e.fails("usage", "use")
	e.fails("usage", "runtime")
	e.fails("usage", "runtime", "add", "x")
	e.fails("usage", "runtime", "remove")
	e.fails("unknown runtime command", "runtime", "frob")
	e.fails("neither a folder", "runtime", "add", "x", "not-a-url")
}

func TestStatusReadsTheRunningSwitcher(t *testing.T) {
	e := setup(t)
	if out := e.ok("status"); !strings.Contains(out, "no switcher is running") {
		t.Errorf("status: %s", out)
	}
	st := switcher.State{PID: 42, Address: "127.0.0.1:2276", Backends: []switcher.BackendState{
		{Name: "official", Port: 1000, Program: "llama"},
		{Name: "prism", Error: "not installed"},
	}}
	data, _ := json.Marshal(st)
	if err := os.MkdirAll(filepath.Join(e.home, "state"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.home, "state", "switcher.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	out := e.ok("status")
	for _, want := range []string{"pid 42", "127.0.0.1:2276", "running", "not installed"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
}

func TestInstallAndUninstallCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("edits the real user PATH on Windows")
	}
	e := setup(t)
	t.Setenv("HOME", t.TempDir())
	if out := e.ok("install", "--skip-official"); !strings.Contains(out, "to the front of the user PATH") {
		t.Errorf("install: %s", out)
	}
	if out := e.ok("uninstall"); !strings.Contains(out, "removed llamaenv's files") {
		t.Errorf("uninstall: %s", out)
	}
}

func TestServeCommandPassesTheOfficialExitCode(t *testing.T) {
	e := setup(t)
	// Without mappings, serve is the official serve; the fake exits at once.
	if code, out := e.run("serve", "--port", "1"); code != 0 {
		t.Errorf("serve: exit %d\n%s", code, out)
	}
	t.Setenv("LLAMAENV_OFFICIAL", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	e.fails("", "serve")
}
