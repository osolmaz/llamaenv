package shim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osolmaz/llamaenv/internal/config"
	"github.com/osolmaz/llamaenv/internal/runtimes"
)

func exe(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, runtimes.Exe("llama"))
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindRealSkipsTheShimsOwnFolder(t *testing.T) {
	own, official := t.TempDir(), t.TempDir()
	shimPath := exe(t, own)
	want := exe(t, official)
	path := strings.Join([]string{own, official}, string(os.PathListSeparator))
	got, err := findReal(path, shimPath, own, nil)
	if err != nil || got != want {
		t.Fatalf("got %q %v, want %q", got, err, want)
	}
}

func TestFindRealSkipsACopyOfItselfElsewhere(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	self := exe(t, a)
	want := exe(t, b)
	got, err := findReal(a+string(os.PathListSeparator)+b, self, "", nil)
	if err != nil || got != want {
		t.Fatalf("got %q %v, want %q", got, err, want)
	}
}

func TestFindRealFallsBackToKnownLocations(t *testing.T) {
	known := exe(t, t.TempDir())
	got, err := findReal(t.TempDir(), "", "", []string{filepath.Join(t.TempDir(), "missing"), known})
	if err != nil || got != known {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := findReal("", "", "", nil); err == nil || !strings.Contains(err.Error(), "llamaenv install") {
		t.Errorf("no official llama: %v", err)
	}
}

func TestNoMappingsMeansPurePassthrough(t *testing.T) {
	t.Setenv("LLAMAENV_HOME", t.TempDir())
	_, needed, err := options("/usr/bin/llama", []string{"--port", "2276"}, func(string) {})
	if err != nil || needed {
		t.Fatalf("needed %v, err %v: without mappings llamaenv must stay out of the way", needed, err)
	}
}

func TestMappingToAMissingRuntimeIsReportedNotFatal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LLAMAENV_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "runtimes.ini"), []byte("[runtime prism]\nmodels = prism-ml/Ternary-Bonsai-2-27B-gguf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opt, needed, err := options("/usr/bin/llama", []string{"--port", "2276"}, func(string) {})
	if err != nil || !needed {
		t.Fatalf("needed %v, err %v", needed, err)
	}
	if opt.Unavailable["prism"] == nil || len(opt.Runtimes) != 0 {
		t.Errorf("unavailable %v, runtimes %v", opt.Unavailable, opt.Runtimes)
	}
	if opt.Owner("prism-ml/Ternary-Bonsai-2-27B-gguf:PQ2_0") != "prism" || opt.Owner("ggml-org/x:Q4_0") != "" {
		t.Error("owner mapping")
	}
}

func TestRuntimeNamesSkipTheDefaultAndDuplicates(t *testing.T) {
	got := runtimeNames([]config.Mapping{
		{Model: "a", Runtime: "prism"}, {Model: "b", Runtime: "official"},
		{Model: "c", Runtime: "prism"}, {Model: "d", Runtime: "pinned"}, {Model: "e", Runtime: "other"},
	}, "pinned")
	if strings.Join(got, ",") != "prism,other" {
		t.Errorf("got %v", got)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMappedRuntimeGetsItsPresetButTheDefaultDoesNot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LLAMAENV_HOME", home)
	dir := t.TempDir()
	write(t, filepath.Join(dir, runtimes.Exe("llama")), "x")
	write(t, filepath.Join(dir, runtimes.Exe("llama-server")), "x")
	write(t, filepath.Join(home, "runtimes.ini"), "[runtime prism]\npath = "+dir+"\nmodels = prism-ml/Ternary-Bonsai-2-27B-gguf\npresets = bonsai-2-27b.ini\n\n"+
		"[runtime pinned]\npath = "+dir+"\npresets = x.ini\n")
	write(t, filepath.Join(home, "llamaenv.ini"), "default = pinned\n")
	var logs []string
	opt, needed, err := options("/usr/bin/llama", []string{"--port", "2276"}, func(s string) { logs = append(logs, s) })
	if err != nil || !needed {
		t.Fatalf("needed %v, err %v", needed, err)
	}
	if got := opt.Presets["prism"]; len(got) != 1 || got[0] != filepath.Join(home, "presets", "prism", "bonsai-2-27b.ini") || len(opt.Presets) != 1 {
		t.Errorf("presets %v", opt.Presets)
	}
	if opt.StateDir != filepath.Join(home, "state", "2276") {
		t.Errorf("state dir %s", opt.StateDir)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "its presets are not used") {
		t.Errorf("logs %v", logs)
	}
}
