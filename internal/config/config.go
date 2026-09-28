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
	Config string // llamaenv.ini, runtimes.ini, models.ini, runtimes.lock
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

// State is the folder for the running switcher's state file.
func (d Dirs) State() string { return filepath.Join(d.Data, "state") }

// Logs is the folder for logs.
func (d Dirs) Logs() string { return filepath.Join(d.Data, "logs") }

// Config holds llamaenv's four config files.
type Config struct {
	Dirs     Dirs
	Settings *File // llamaenv.ini: default runtime and switcher settings
	Runtimes *File // runtimes.ini: runtime sources
	Models   *File // models.ini: which model uses which runtime
	Lock     *File // runtimes.lock: SHA-256 of every downloaded archive
}

const (
	settingsFile = "llamaenv.ini"
	runtimesFile = "runtimes.ini"
	modelsFile   = "models.ini"
	lockFile     = "runtimes.lock"
)

// Load reads the config files. Missing files are empty.
func Load(d Dirs) (*Config, error) {
	c := &Config{Dirs: d}
	for name, dst := range map[string]**File{
		settingsFile: &c.Settings, runtimesFile: &c.Runtimes, modelsFile: &c.Models, lockFile: &c.Lock,
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

// Save writes all four files.
func (c *Config) Save() error {
	if err := os.MkdirAll(c.Dirs.Config, 0o750); err != nil {
		return err
	}
	for name, f := range map[string]*File{
		settingsFile: c.Settings, runtimesFile: c.Runtimes, modelsFile: c.Models, lockFile: c.Lock,
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

// Mapping is one models.ini entry.
type Mapping struct {
	Model   string // "<repo>" or "<repo>:<quant>"
	Runtime string
}

// Mappings returns the models.ini entries, sorted by model.
func (c *Config) Mappings() []Mapping {
	var m []Mapping
	for _, s := range c.Models.Sections() {
		if rt, ok := s.Get("runtime"); ok && rt != "" {
			m = append(m, Mapping{Model: s.Name, Runtime: rt})
		}
	}
	sort.Slice(m, func(i, j int) bool { return strings.ToLower(m[i].Model) < strings.ToLower(m[j].Model) })
	return m
}

// RuntimeFor returns the runtime mapped to a model ID ("<repo>:<quant>").
// An entry for the exact ID wins over an entry for the whole repo.
func (c *Config) RuntimeFor(modelID string) (string, bool) {
	if modelID == "" {
		return "", false
	}
	lookup := func(name string) (string, bool) {
		if s := c.Models.Section(name); s != nil && s.Name != "" {
			if rt, ok := s.Get("runtime"); ok && rt != "" {
				return rt, true
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
