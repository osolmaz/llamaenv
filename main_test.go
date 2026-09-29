package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osolmaz/llamaenv/internal/proc"
)

func TestMain(m *testing.M) {
	if os.Getenv("FAKE_LLAMA") == "1" {
		os.Exit(7)
	}
	os.Exit(m.Run())
}

func TestTheProgramNameChoosesShimOrCommand(t *testing.T) {
	t.Setenv("LLAMAENV_HOME", t.TempDir())
	t.Setenv("LLAMAENV_OFFICIAL", os.Args[0])
	t.Setenv("FAKE_LLAMA", "1")
	if code := dispatch(filepath.Join("x", "LLAMA.EXE"), []string{"--version"}); code != 7 {
		t.Errorf("as llama.exe: exit %d, want the official llama's 7", code)
	}
	if code := dispatch("/usr/bin/llama", []string{"--version"}); code != 7 {
		t.Errorf("as llama: exit %d", code)
	}
	if code := dispatch("/usr/bin/llamaenv", []string{"version"}); code != 0 {
		t.Errorf("as llamaenv: exit %d", code)
	}
}

func TestTheWatchCommandComesBeforeTheShim(t *testing.T) {
	t.Setenv("LLAMAENV_OFFICIAL", os.Args[0])
	t.Setenv("FAKE_LLAMA", "1")
	// Without a parent pid and a program, the watcher answers 2 on every
	// system; the official llama would answer 7.
	for _, program := range []string{"/x/llama", "/x/llamaenv"} {
		if code := dispatch(program, []string{proc.WatchCommand}); code != 2 {
			t.Errorf("%s %s: exit %d, want the watcher's 2", program, proc.WatchCommand, code)
		}
	}
}
