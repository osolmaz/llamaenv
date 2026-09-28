//go:build !windows

package install

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/osolmaz/llamaenv/internal/config"
	"github.com/osolmaz/llamaenv/internal/switcher"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

// installed sets up a home with an official llama and installs llamaenv.
func installed(t *testing.T) (string, config.Dirs, *[]string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	official := filepath.Join(t.TempDir(), "llama")
	must(t, os.WriteFile(official, []byte("x"), 0o600))
	t.Setenv("LLAMAENV_OFFICIAL", official)
	d := config.Dirs{Config: filepath.Join(home, "cfg"), Data: filepath.Join(home, "data")}
	log := &[]string{}
	must(t, Install(d, Options{SkipOfficial: true, Log: func(s string) { *log = append(*log, s) }}))
	return home, d, log
}

func TestInstallAddsTheShimFirstOnPath(t *testing.T) {
	home, d, log := installed(t)
	for _, name := range []string{"llama", "llamaenv"} {
		if st, err := os.Stat(filepath.Join(d.Bin(), name)); err != nil || st.Mode()&0o100 == 0 {
			t.Errorf("%s not installed as a program: %v", name, err)
		}
	}
	if !strings.Contains(read(t, filepath.Join(home, ".profile")), d.Bin()) {
		t.Error(".profile does not put the bin folder on PATH")
	}
	// Installing again replaces the programs in place.
	must(t, Install(d, Options{SkipOfficial: true, Log: func(string) {}}))
	if len(*log) == 0 {
		t.Error("install logged nothing")
	}
}

func TestUninstallLeavesNoTrace(t *testing.T) {
	home, d, _ := installed(t)
	// A running switcher is stopped on uninstall.
	sleeper := exec.CommandContext(context.Background(), "sleep", "60")
	must(t, sleeper.Start())
	exited := make(chan struct{})
	go func() { _ = sleeper.Wait(); close(exited) }()
	st, err := json.Marshal(switcher.State{PID: sleeper.Process.Pid})
	must(t, err)
	must(t, os.MkdirAll(d.State(), 0o750))
	must(t, os.WriteFile(filepath.Join(d.State(), "switcher.json"), st, 0o600))
	must(t, os.MkdirAll(d.Config, 0o750))
	must(t, os.WriteFile(filepath.Join(d.Config, "models.ini"), []byte("[a/b]\nruntime = x\n"), 0o600))

	var log []string
	must(t, Uninstall(d, func(s string) { log = append(log, s) }))
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Error("the running switcher was not stopped")
	}
	for _, p := range []string{d.Config, d.Data} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
	if strings.Contains(read(t, filepath.Join(home, ".profile")), "llamaenv") {
		t.Error(".profile still mentions llamaenv")
	}
	if !strings.Contains(strings.Join(log, "\n"), "model files in the Hugging Face cache stay") {
		t.Errorf("log: %v", log)
	}
}

func TestInstallNeedsTheOfficialLlamaWhenSkipped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LLAMAENV_OFFICIAL", "")
	t.Setenv("PATH", t.TempDir())
	d := config.Dirs{Config: t.TempDir(), Data: t.TempDir()}
	if err := Install(d, Options{SkipOfficial: true, Log: func(string) {}}); err == nil {
		t.Error("installed without an official llama")
	}
}

func TestUninstallWithoutStateOrProfilesWorks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := config.Dirs{Config: filepath.Join(t.TempDir(), "c"), Data: filepath.Join(t.TempDir(), "d")}
	must(t, Uninstall(d, func(string) {}))
}
