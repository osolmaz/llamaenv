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
	if err := os.WriteFile(filepath.Join(home, "models.ini"), []byte("[prism-ml/Ternary-Bonsai-2-27B-gguf]\nruntime = prism\n"), 0o600); err != nil {
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
