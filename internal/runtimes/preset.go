package runtimes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/osolmaz/llamaenv/internal/config"
)

// maxPresetSize bounds a preset file. Real presets are a few kilobytes.
const maxPresetSize = 1 << 20

// SetPreset copies a llama.cpp preset from a file or an http(s) URL into
// llamaenv's presets folder, unchanged, and gives it to a runtime. A URL is
// pinned in runtimes.lock like a runtime archive. It returns the saved path.
func SetPreset(c *config.Config, name, src string, log func(string)) (string, error) {
	if c.Runtime(name) == nil || name == config.Official {
		return "", fmt.Errorf("unknown runtime %q; only an added runtime can have a preset", name)
	}
	data, err := readPreset(c, src, log)
	if err != nil {
		return "", err
	}
	if _, err := config.ParseINI(string(data)); err != nil {
		return "", fmt.Errorf("%s is not a llama.cpp preset: %w", src, err)
	}
	if err := os.MkdirAll(c.Dirs.Presets(), 0o750); err != nil {
		return "", err
	}
	dst := filepath.Join(c.Dirs.Presets(), name+".ini")
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return "", err
	}
	c.Runtimes.Set(config.RuntimeSection(name), "preset", "presets/"+name+".ini")
	return dst, nil
}

func readPreset(c *config.Config, src string, log func(string)) ([]byte, error) {
	path := src
	if strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://") {
		file, err := downloadPreset(c, src, log)
		if err != nil {
			return nil, err
		}
		defer func() { _ = os.Remove(file) }()
		path = file
	}
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("preset %s does not exist", src)
	}
	if err != nil {
		return nil, err
	}
	if st.Size() > maxPresetSize {
		return nil, fmt.Errorf("%s is larger than %d bytes; a preset is a small INI file", src, maxPresetSize)
	}
	return os.ReadFile(filepath.Clean(path))
}

// downloadPreset downloads a preset and checks it against runtimes.lock. The
// first download records its SHA-256; later downloads must match it.
func downloadPreset(c *config.Config, url string, log func(string)) (string, error) {
	file, sum, err := download(c.Dirs.Data, url, log)
	if err != nil {
		return "", err
	}
	if want, ok := c.Lock.Get(url, "sha256"); ok && !strings.EqualFold(want, sum) {
		_ = os.Remove(file)
		return "", fmt.Errorf("%s: SHA-256 %s does not match the pinned %s in runtimes.lock", url, sum, want)
	}
	c.Lock.Set(url, "sha256", sum)
	return file, nil
}

// RemovePreset deletes a runtime's preset, when it has one.
func RemovePreset(c *config.Config, name string) error {
	s := c.Runtime(name)
	p := c.Preset(name)
	if s == nil || p == "" {
		return nil
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	s.Delete("preset")
	return nil
}
