//go:build !windows

package install

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	blockStart = "# >>> llamaenv >>>"
	blockEnd   = "# <<< llamaenv <<<"
)

// addToPath adds a marked block to the shell profiles that puts dir first on
// PATH. New shells and programs started from them pick it up.
func addToPath(dir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	block := blockStart + "\nexport PATH=\"" + dir + ":$PATH\"\n" + blockEnd + "\n"
	for _, f := range profiles(home, true) {
		if err := editBlock(f, block); err != nil {
			return err
		}
	}
	return nil
}

func removeFromPath(string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for _, f := range profiles(home, false) {
		if err := editBlock(f, ""); err != nil {
			return err
		}
	}
	return nil
}

// profiles returns ~/.profile (created when create is set) and the existing
// ~/.bashrc and ~/.zshrc.
func profiles(home string, create bool) []string {
	out := []string{}
	p := filepath.Join(home, ".profile")
	if _, err := os.Stat(p); err == nil || create {
		out = append(out, p)
	}
	for _, name := range []string{".bashrc", ".zshrc"} {
		if _, err := os.Stat(filepath.Join(home, name)); err == nil {
			out = append(out, filepath.Join(home, name))
		}
	}
	return out
}

// editBlock replaces llamaenv's block in a file, or removes it when block is
// empty. Everything else in the file, and the file's permissions, stay.
func editBlock(path, block string) error {
	path = filepath.Clean(path)
	mode := os.FileMode(0o600)
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if st, serr := os.Stat(path); serr == nil {
			mode = st.Mode().Perm()
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	text := withoutBlock(string(data))
	if block != "" {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += block
	}
	if text == string(data) {
		return nil
	}
	// G703: path is one of the user's own shell profiles, from profiles().
	return os.WriteFile(path, []byte(text), mode) //nolint:gosec // the user's shell profile, see above
}

// withoutBlock removes llamaenv's marked block from text.
func withoutBlock(text string) string {
	i := strings.Index(text, blockStart)
	if i < 0 {
		return text
	}
	j := strings.Index(text[i:], blockEnd)
	if j < 0 {
		return text
	}
	end := i + j + len(blockEnd)
	if end < len(text) && text[end] == '\n' {
		end++
	}
	return text[:i] + text[end:]
}

func replaceFile(tmp, dst string) error { return os.Rename(tmp, dst) }

func removeDataDir(dir string) error { return os.RemoveAll(dir) }
