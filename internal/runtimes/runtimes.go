// Package runtimes resolves and installs llama.cpp runtimes.
package runtimes

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/osolmaz/llamaenv/internal/config"
)

// Runtime is an installed llama.cpp build.
type Runtime struct {
	Name   string
	Dir    string
	Llama  string // unified "llama" program, empty when the build has none
	Server string // "llama-server" program, empty when the build has none
}

// Launch returns the program and leading arguments that start this runtime
// as a router. The unified program needs the "serve" subcommand.
func (r Runtime) Launch() (string, []string) {
	if r.Llama != "" {
		return r.Llama, []string{"serve"}
	}
	return r.Server, nil
}

// CanBeDefault says whether the runtime can serve every unmapped model: only
// a build with the unified llama program behaves like the standard one.
func (r Runtime) CanBeDefault() bool { return r.Llama != "" }

// Platform is the key for this machine's sources in runtimes.ini.
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// Exe adds ".exe" on Windows.
func Exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// Resolve finds an installed runtime by name.
func Resolve(c *config.Config, name string) (Runtime, error) {
	s := c.Runtime(name)
	if s == nil || name == config.Official {
		return Runtime{}, fmt.Errorf("unknown runtime %q; add it with 'llamaenv runtime add'", name)
	}
	dir := filepath.Join(c.Dirs.Runtimes(), name)
	if p, ok := s.Get("path"); ok && p != "" {
		dir = p
	}
	rt, err := scan(dir)
	if err != nil {
		return Runtime{}, fmt.Errorf("runtime %q: %w", name, err)
	}
	rt.Name = name
	return rt, nil
}

// scan looks for the llama programs in dir and up to three levels below it,
// shallowest first. Folders that cannot be read are skipped.
func scan(dir string) (Runtime, error) {
	if _, err := os.Stat(dir); err != nil {
		return Runtime{}, fmt.Errorf("not installed (%s is missing)", dir)
	}
	rt := Runtime{Dir: dir}
	level := []string{dir}
	for depth := 0; depth <= 3 && len(level) > 0; depth++ {
		var next []string
		for _, d := range level {
			next = append(next, rt.look(d)...)
		}
		level = next
	}
	if rt.Llama == "" && rt.Server == "" {
		return Runtime{}, fmt.Errorf("no %s or %s in %s", Exe("llama"), Exe("llama-server"), dir)
	}
	return rt, nil
}

// look records the programs in one folder and returns its subfolders.
func (rt *Runtime) look(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var subdirs []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir():
			subdirs = append(subdirs, p)
		case e.Name() == Exe("llama") && rt.Llama == "":
			rt.Llama = p
		case e.Name() == Exe("llama-server") && rt.Server == "":
			rt.Server = p
		}
	}
	return subdirs
}

// Add registers a runtime. A single existing folder is used in place;
// otherwise the sources are archive URLs for this platform, downloaded,
// checked, and unpacked into llamaenv's runtimes folder.
func Add(c *config.Config, name string, sources []string, log func(string)) (Runtime, error) {
	if name == "" || name == config.Official || strings.ContainsAny(name, `/\:[]`) {
		return Runtime{}, fmt.Errorf("invalid runtime name %q", name)
	}
	if len(sources) == 0 {
		return Runtime{}, errors.New("give a folder or one or more archive URLs")
	}
	if st, err := os.Stat(sources[0]); err == nil && st.IsDir() && len(sources) == 1 {
		return addFolder(c, name, sources[0])
	}
	return addArchives(c, name, sources, log)
}

func addFolder(c *config.Config, name, folder string) (Runtime, error) {
	abs, err := filepath.Abs(folder)
	if err != nil {
		return Runtime{}, err
	}
	rt, err := scan(abs)
	if err != nil {
		return Runtime{}, err
	}
	setSource(c, name, "path", abs)
	rt.Name = name
	return rt, nil
}

func addArchives(c *config.Config, name string, urls []string, log func(string)) (Runtime, error) {
	for _, u := range urls {
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return Runtime{}, fmt.Errorf("%q is neither a folder nor an http(s) URL", u)
		}
	}
	dest := filepath.Join(c.Dirs.Runtimes(), name)
	if err := installArchives(c, dest, urls, log); err != nil {
		return Runtime{}, err
	}
	rt, err := scan(dest)
	if err != nil {
		return Runtime{}, err
	}
	setSource(c, name, Platform(), strings.Join(urls, " "))
	rt.Name = name
	return rt, nil
}

// setSource replaces a runtime's sources and keeps its models and presets.
func setSource(c *config.Config, name, key, value string) {
	sec := config.RuntimeSection(name)
	kept := map[string]string{}
	if s := c.Runtime(name); s != nil {
		for _, k := range []string{"models", "presets"} {
			if v, ok := s.Get(k); ok {
				kept[k] = v
			}
		}
	}
	c.Runtimes.DeleteSection(sec)
	c.Runtimes.Set(sec, key, value)
	for _, k := range []string{"models", "presets"} {
		if v, ok := kept[k]; ok {
			c.Runtimes.Set(sec, k, v)
		}
	}
}

// Install downloads a registered runtime for this platform when it is missing.
func Install(c *config.Config, name string, log func(string)) (Runtime, error) {
	if rt, err := Resolve(c, name); err == nil {
		return rt, nil
	}
	src, ok := c.Runtimes.Get(config.RuntimeSection(name), Platform())
	if !ok || src == "" {
		return Runtime{}, fmt.Errorf("runtime %q has no source for %s", name, Platform())
	}
	dest := filepath.Join(c.Dirs.Runtimes(), name)
	if err := installArchives(c, dest, strings.Fields(src), log); err != nil {
		return Runtime{}, err
	}
	return Resolve(c, name)
}

// Remove deletes a runtime's files (never a folder used in place), its
// presets, and its config entry.
func Remove(c *config.Config, name string) error {
	s := c.Runtime(name)
	if s == nil || name == config.Official {
		return fmt.Errorf("unknown runtime %q", name)
	}
	if _, inPlace := s.Get("path"); !inPlace {
		if err := os.RemoveAll(filepath.Join(c.Dirs.Runtimes(), name)); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(c.Dirs.Presets(name)); err != nil {
		return err
	}
	c.Runtimes.DeleteSection(config.RuntimeSection(name))
	return nil
}

// Names returns the added runtimes.
func Names(c *config.Config) []string { return c.RuntimeNames() }
