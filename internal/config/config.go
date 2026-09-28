package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Official is the built-in runtime: the llama that the standard installer
// manages. llamaenv never downloads, updates, or moves it.
const Official = "official"

// Dirs are llamaenv's folders. On Windows both are %LOCALAPPDATA%\llamaenv.
type Dirs struct {
	Config string // llamaenv.ini, runtimes.ini, runtimes.lock, presets
	Data   string // bin, runtimes, downloads, logs, state
}

// DefaultDirs returns the folders for this user. LLAMAENV_HOME puts both in
// one folder, for tests and portable setups.
func DefaultDirs() (Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil && runtime.GOOS != "windows" && os.Getenv("LLAMAENV_HOME") == "" {
		return Dirs{}, err
	}
	return dirsFor(runtime.GOOS, os.Getenv, home)
}

// dirsFor picks the folders for one operating system and environment.
func dirsFor(goos string, getenv func(string) string, home string) (Dirs, error) {
	if h := getenv("LLAMAENV_HOME"); h != "" {
		return Dirs{Config: h, Data: h}, nil
	}
	if goos == "windows" {
		base := getenv("LOCALAPPDATA")
		if base == "" {
			return Dirs{}, errors.New("LOCALAPPDATA is not set")
		}
		d := filepath.Join(base, "llamaenv")
		return Dirs{Config: d, Data: d}, nil
	}
	return Dirs{
		Config: filepath.Join(orDefault(getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config")), "llamaenv"),
		Data:   filepath.Join(orDefault(getenv("XDG_DATA_HOME"), filepath.Join(home, ".local", "share")), "llamaenv"),
	}, nil
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// Bin is the folder that holds the llama shim and llamaenv itself.
func (d Dirs) Bin() string { return filepath.Join(d.Data, "bin") }

// Runtimes is the folder that holds downloaded runtimes.
func (d Dirs) Runtimes() string { return filepath.Join(d.Data, "runtimes") }

// State holds one folder per running switcher, named after its port.
func (d Dirs) State() string { return filepath.Join(d.Data, "state") }

// Logs is the folder for logs.
func (d Dirs) Logs() string { return filepath.Join(d.Data, "logs") }

// Presets is the folder for the llama.cpp presets that runtimes use.
func (d Dirs) Presets() string { return filepath.Join(d.Config, "presets") }

// Config holds llamaenv's config files.
type Config struct {
	Dirs     Dirs
	Settings *File // llamaenv.ini: default runtime and switcher settings
	Runtimes *File // runtimes.ini: runtime sources, their models, and their presets
	Lock     *File // runtimes.lock: SHA-256 of every download
}

const (
	settingsFile = "llamaenv.ini"
	runtimesFile = "runtimes.ini"
	lockFile     = "runtimes.lock"
)

// Load reads the config files. Missing files are empty.
func Load(d Dirs) (*Config, error) {
	c := &Config{Dirs: d}
	for name, dst := range map[string]**File{
		settingsFile: &c.Settings, runtimesFile: &c.Runtimes, lockFile: &c.Lock,
	} {
		f, err := readINI(filepath.Join(d.Config, name))
		if err != nil {
			return nil, err
		}
		*dst = f
	}
	return c, nil
}

func readINI(path string) (*File, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return ParseINI("")
	}
	if err != nil {
		return nil, err
	}
	f, err := ParseINI(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Save writes all config files.
func (c *Config) Save() error {
	if err := os.MkdirAll(c.Dirs.Config, 0o750); err != nil {
		return err
	}
	for name, f := range map[string]*File{
		settingsFile: c.Settings, runtimesFile: c.Runtimes, lockFile: c.Lock,
	} {
		if err := writeAtomic(filepath.Join(c.Dirs.Config, name), f.String()); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(path, text string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Default is the runtime for models without a mapping.
func (c *Config) Default() string {
	if v, ok := c.Settings.Get("", "default"); ok && v != "" {
		return v
	}
	return Official
}

// Exclusive says whether only one runtime may hold a loaded model at a time.
// It is on by default, because two models rarely fit in memory together.
func (c *Config) Exclusive() bool {
	v, ok := c.Settings.Get("", "exclusive")
	return !ok || (!strings.EqualFold(v, "false") && v != "0" && !strings.EqualFold(v, "no"))
}

// runtimes.ini has one "[runtime <name>]" section per runtime. The sections
// name runtimes, not models, so the file cannot be taken for a llama.cpp
// preset.
const runtimePrefix = "runtime "

// RuntimeSection returns the section name of a runtime in runtimes.ini.
func RuntimeSection(name string) string { return runtimePrefix + name }

// Runtime returns a runtime's section in runtimes.ini, or nil.
func (c *Config) Runtime(name string) *Section {
	if name == "" {
		return nil
	}
	return c.Runtimes.Section(RuntimeSection(name))
}

// RuntimeNames returns the added runtimes in file order. The official
// runtime is built in and never listed, even when it holds mappings.
func (c *Config) RuntimeNames() []string {
	var names []string
	for _, s := range c.Runtimes.Sections() {
		if n, ok := runtimeName(s); ok && n != Official {
			names = append(names, n)
		}
	}
	return names
}

func runtimeName(s *Section) (string, bool) {
	if len(s.Name) <= len(runtimePrefix) || !strings.EqualFold(s.Name[:len(runtimePrefix)], runtimePrefix) {
		return "", false
	}
	return strings.TrimSpace(s.Name[len(runtimePrefix):]), true
}

// Mapping is one model in a runtime's "models" list.
type Mapping struct {
	Model   string // "<repo>" or "<repo>:<quant>"
	Runtime string
}

// Mappings returns every mapped model, sorted by model.
func (c *Config) Mappings() []Mapping {
	var m []Mapping
	for _, s := range c.Runtimes.Sections() {
		name, ok := runtimeName(s)
		if !ok {
			continue
		}
		for _, model := range modelList(s) {
			m = append(m, Mapping{Model: model, Runtime: name})
		}
	}
	sort.Slice(m, func(i, j int) bool { return strings.ToLower(m[i].Model) < strings.ToLower(m[j].Model) })
	return m
}

func modelList(s *Section) []string {
	v, _ := s.Get("models")
	return strings.Fields(v)
}

// RuntimeFor returns the runtime mapped to a model ID ("<repo>:<quant>").
// An entry for the exact ID wins over an entry for the whole repo. Case is
// ignored, as on the Hugging Face Hub. These are llamaenv's matching rules,
// not llama.cpp's preset rules.
func (c *Config) RuntimeFor(modelID string) (string, bool) {
	if modelID == "" {
		return "", false
	}
	lookup := func(name string) (string, bool) {
		for _, m := range c.Mappings() {
			if strings.EqualFold(m.Model, name) {
				return m.Runtime, true
			}
		}
		return "", false
	}
	if rt, ok := lookup(modelID); ok {
		return rt, true
	}
	if repo, _, found := strings.Cut(modelID, ":"); found {
		return lookup(repo)
	}
	return "", false
}

// Map moves a model to a runtime's "models" list.
func (c *Config) Map(model, runtime string) {
	c.Unmap(model)
	s := c.Runtimes.Section(RuntimeSection(runtime))
	var models []string
	if s != nil {
		models = modelList(s)
	}
	c.Runtimes.Set(RuntimeSection(runtime), "models", strings.Join(append(models, model), " "))
}

// Unmap removes a model from every "models" list and reports whether it was
// mapped. An official section without models goes away.
func (c *Config) Unmap(model string) bool {
	found := false
	for _, s := range c.Runtimes.Sections() {
		name, ok := runtimeName(s)
		if !ok {
			continue
		}
		var keep []string
		for _, m := range modelList(s) {
			if strings.EqualFold(m, model) {
				found = true
			} else {
				keep = append(keep, m)
			}
		}
		if len(keep) > 0 {
			s.Set("models", strings.Join(keep, " "))
			continue
		}
		s.Delete("models")
		if name == Official && len(s.Keys()) == 0 {
			c.Runtimes.DeleteSection(s.Name)
		}
	}
	return found
}

// Preset returns the full path of a runtime's llama.cpp preset, or "".
func (c *Config) Preset(runtime string) string {
	s := c.Runtime(runtime)
	if s == nil {
		return ""
	}
	p, _ := s.Get("preset")
	if p == "" {
		return ""
	}
	return filepath.Join(c.Dirs.Config, filepath.FromSlash(p))
}
