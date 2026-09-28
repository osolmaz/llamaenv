//go:build !windows

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathBlockIsAddedOnceAndRemovedCleanly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bashrc := filepath.Join(home, ".bashrc")
	original := "alias ll='ls -l'\nexport EDITOR=vim"
	if err := os.WriteFile(bashrc, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // installing twice keeps one block
		if err := addToPath("/opt/llamaenv/bin"); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{bashrc, filepath.Join(home, ".profile")} {
		data, _ := os.ReadFile(filepath.Clean(f))
		if n := strings.Count(string(data), blockStart); n != 1 {
			t.Errorf("%s has %d blocks:\n%s", f, n, data)
		}
		if !strings.Contains(string(data), `export PATH="/opt/llamaenv/bin:$PATH"`) {
			t.Errorf("%s has no PATH line:\n%s", f, data)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); err == nil {
		t.Error("created a .zshrc that did not exist")
	}
	if err := removeFromPath("/opt/llamaenv/bin"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Clean(bashrc))
	if strings.TrimRight(string(data), "\n") != original {
		t.Errorf("after removal:\n%q\nwant\n%q", data, original)
	}
}
