package runtimes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/osolmaz/llamaenv/internal/config"
)

// maxPresetSize bounds a preset file. Real presets are a few kilobytes.
const maxPresetSize = 1 << 20

// AddPreset copies a llama.cpp preset from a file or an http(s) URL into the
// runtime's presets folder, unchanged, under its own file name. A preset with
// the same name is replaced; others stay. A URL is pinned in runtimes.lock
// like a runtime archive. It returns the saved path.
func AddPreset(c *config.Config, runtime, src string, log func(string)) (string, error) {
	if c.Runtime(runtime) == nil || runtime == config.Official {
		return "", fmt.Errorf("unknown runtime %q; only an added runtime can have presets", runtime)
	}
	name, err := presetName(src)
	if err != nil {
		return "", err
	}
	data, err := readPreset(c, src, log)
	if err != nil {
		return "", err
	}
	if _, err := config.ParseINI(string(data)); err != nil {
		return "", fmt.Errorf("%s is not a llama.cpp preset: %w", src, err)
	}
	return savePreset(c, runtime, name, data)
}

// savePreset writes a preset into the runtime's presets folder and lists it.
func savePreset(c *config.Config, runtime, name string, data []byte) (string, error) {
	dir := c.Dirs.Presets(runtime)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, name)
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return "", err
	}
	names := c.PresetNames(runtime)
	if !slices.Contains(names, name) {
		c.Runtimes.Set(config.RuntimeSection(runtime), "presets", strings.Join(append(names, name), " "))
	}
	return dst, nil
}

// presetName is the file name of a preset source, which becomes its name.
func presetName(src string) (string, error) {
	name := filepath.Base(src)
	if strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://") {
		name = path.Base(strings.SplitN(src, "?", 2)[0])
	}
	if !strings.HasSuffix(strings.ToLower(name), ".ini") || strings.ContainsAny(name, " \t") || name == ".ini" {
		return "", fmt.Errorf("%q: a preset file name must end in .ini and have no spaces, for example bonsai-2-27b.ini", name)
	}
	return name, nil
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

// RemovePreset deletes one of a runtime's presets.
func RemovePreset(c *config.Config, runtime, name string) error {
	names := c.PresetNames(runtime)
	i := slices.Index(names, name)
	if i < 0 {
		return fmt.Errorf("runtime %s has no preset %s; see 'llamaenv list'", runtime, name)
	}
	if err := os.Remove(filepath.Join(c.Dirs.Presets(runtime), name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	names = slices.Delete(names, i, i+1)
	if len(names) == 0 {
		c.Runtime(runtime).Delete("presets")
		return nil
	}
	c.Runtimes.Set(config.RuntimeSection(runtime), "presets", strings.Join(names, " "))
	return nil
}
