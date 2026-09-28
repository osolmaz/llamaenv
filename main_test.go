package main

import (
	"os"
	"path/filepath"
	"testing"
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
