// Package shim is the "llama" program that llamaenv puts first on PATH.
//
// "llama serve" starts the switcher when a model mapping or a non-default
// runtime needs it. Every other command, and "llama serve" without mappings,
// runs the official llama unchanged. When the switcher cannot start, the shim
// runs the official "llama serve" instead: the standard path always works.
package shim

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/osolmaz/llamaenv/internal/config"
	"github.com/osolmaz/llamaenv/internal/proc"
	"github.com/osolmaz/llamaenv/internal/runtimes"
	"github.com/osolmaz/llamaenv/internal/switcher"
)

// Main runs the shim with the arguments after the program name and returns
// the exit code.
func Main(args []string) int {
	real, err := RealLlama()
	if err != nil {
		fmt.Fprintln(os.Stderr, "llamaenv:", err)
		return 127
	}
	if len(args) == 0 || args[0] != "serve" {
		return passthrough(real, args)
	}
	return Serve(real, args)
}

// Serve runs "llama serve" through the switcher, or passes it on to the
// official llama when nothing needs switching or the switcher fails.
func Serve(real string, args []string) int {
	logf := func(msg string) { fmt.Fprintln(os.Stderr, "llamaenv: "+msg) }
	opt, needed, err := options(real, args[1:], logf)
	if err != nil {
		logf("config error, running the official llama serve instead: " + err.Error())
		return passthrough(real, args)
	}
	if !needed {
		return passthrough(real, args)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = switcher.Run(ctx, opt)
	switch {
	case err == nil || errors.Is(err, context.Canceled):
		return 0
	case errors.Is(err, switcher.ErrSetup):
		logf(err.Error() + "; running the official llama serve instead")
		return passthrough(real, args)
	default:
		logf(err.Error())
		return 1
	}
}

// options builds the switcher's options from the config. needed is false
// when no mapping and no custom default exist: then llamaenv stays out of
// the way completely.
func options(real string, serveArgs []string, logf func(string)) (switcher.Options, bool, error) {
	dirs, c, sa, err := load(serveArgs)
	if err != nil {
		return switcher.Options{}, false, err
	}
	mappings := c.Mappings()
	if len(mappings) == 0 && c.Default() == config.Official {
		return switcher.Options{}, false, nil
	}
	opt := switcher.Options{
		Args:        sa,
		Default:     switcher.Launch{Program: real, Prefix: []string{"serve"}},
		DefaultName: config.Official,
		Runtimes:    map[string]switcher.Launch{},
		Presets:     map[string][]string{},
		Unavailable: map[string]error{},
		Exclusive:   c.Exclusive(),
		StateDir:    filepath.Join(dirs.State(), strconv.Itoa(sa.Port)),
		LogDir:      switcher.LogDir(dirs.Logs(), sa.Port),
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Log:         logf,
		Owner: func(model string) string {
			rt, _ := c.RuntimeFor(model)
			if rt == config.Official {
				return ""
			}
			return rt
		},
	}
	if err := setDefault(c, &opt); err != nil {
		return opt, false, err
	}
	if len(c.Presets(opt.DefaultName)) > 0 {
		logf("the default runtime " + opt.DefaultName + " serves every model, so its presets are not used")
	}
	addRuntimes(c, mappings, &opt)
	return opt, true, nil
}

// load reads the config and the serve arguments.
func load(serveArgs []string) (config.Dirs, *config.Config, switcher.ServeArgs, error) {
	dirs, err := config.DefaultDirs()
	if err != nil {
		return dirs, nil, switcher.ServeArgs{}, err
	}
	c, err := config.Load(dirs)
	if err != nil {
		return dirs, nil, switcher.ServeArgs{}, err
	}
	sa, err := switcher.ParseServeArgs(serveArgs)
	return dirs, c, sa, err
}

// setDefault replaces the official router with the configured default
// runtime, when there is one.
func setDefault(c *config.Config, opt *switcher.Options) error {
	d := c.Default()
	if d == config.Official {
		return nil
	}
	rt, err := runtimes.Resolve(c, d)
	if err != nil {
		return fmt.Errorf("default runtime: %w", err)
	}
	if !rt.CanBeDefault() {
		return fmt.Errorf("default runtime %s has no %s, so it cannot serve every model", d, runtimes.Exe("llama"))
	}
	prog, prefix := rt.Launch()
	opt.Default, opt.DefaultName = switcher.Launch{Program: prog, Prefix: prefix}, d
	return nil
}

// addRuntimes adds one router per mapped runtime. A runtime that cannot be
// resolved is reported per request; it does not stop the switcher.
func addRuntimes(c *config.Config, mappings []config.Mapping, opt *switcher.Options) {
	for _, name := range runtimeNames(mappings, opt.DefaultName) {
		rt, err := runtimes.Resolve(c, name)
		if err != nil {
			opt.Unavailable[name] = err
			continue
		}
		prog, prefix := rt.Launch()
		opt.Runtimes[name] = switcher.Launch{Program: prog, Prefix: prefix}
		if p := c.Presets(name); len(p) > 0 {
			opt.Presets[name] = p
		}
	}
}

// runtimeNames lists the mapped runtimes once each, without the ones that
// the default router already serves.
func runtimeNames(mappings []config.Mapping, defaultName string) []string {
	seen := map[string]bool{config.Official: true, defaultName: true}
	var names []string
	for _, m := range mappings {
		if !seen[m.Runtime] {
			seen[m.Runtime] = true
			names = append(names, m.Runtime)
		}
	}
	return names
}

// passthrough runs the official llama with the same arguments and returns
// its exit code. The child dies with the shim, so a hard stop of the shim
// also stops it.
func passthrough(real string, args []string) int {
	group, err := proc.NewGroup()
	if err != nil {
		fmt.Fprintln(os.Stderr, "llamaenv:", err)
		return 1
	}
	// G204: runs the official llama that the shim stands in for.
	cmd := exec.CommandContext(context.Background(), real, args...) //nolint:gosec // the official llama, see above
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := group.Start(cmd); err != nil {
		fmt.Fprintln(os.Stderr, "llamaenv:", err)
		return 127
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		for s := range sig {
			proc.Signal(cmd, s)
		}
	}()
	err = cmd.Wait()
	signal.Stop(sig)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

// RealLlama finds the official llama: on macOS the one that the Llama app
// ran before llamaenv took its place, else the first "llama" on PATH that is
// not llamaenv's own shim, or llama.app's install location.
func RealLlama() (string, error) {
	if p := os.Getenv("LLAMAENV_OFFICIAL"); p != "" {
		return p, nil
	}
	self, _ := os.Executable()
	own := ""
	if dirs, err := config.DefaultDirs(); err == nil {
		own = dirs.Bin()
		if kept := filepath.Join(dirs.Official(), "llama"); isOtherProgram(kept, self) {
			return kept, nil
		}
	}
	return findReal(os.Getenv("PATH"), self, own, knownLocations())
}

func findReal(pathEnv, self, ownDir string, known []string) (string, error) {
	for _, c := range append(pathCandidates(pathEnv, ownDir), known...) {
		if isOtherProgram(filepath.Clean(c), self) {
			return filepath.Clean(c), nil
		}
	}
	return "", errors.New("the official llama is not installed; run 'llamaenv install', or install it from https://llama.app")
}

// pathCandidates lists "llama" in every PATH folder except the shim's own.
func pathCandidates(pathEnv, ownDir string) []string {
	var out []string
	for _, dir := range filepath.SplitList(pathEnv) {
		dir = strings.Trim(strings.TrimSpace(dir), `"`)
		if dir != "" && !sameDir(dir, ownDir) {
			out = append(out, filepath.Join(dir, runtimes.Exe("llama")))
		}
	}
	return out
}

// isOtherProgram says whether path is a file that is not the running shim.
func isOtherProgram(path, self string) bool {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return false
	}
	me, err := os.Stat(self)
	return err != nil || !os.SameFile(me, st)
}

func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ca, cb)
	}
	return ca == cb
}

func knownLocations() []string {
	if runtime.GOOS == "windows" {
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			return []string{filepath.Join(base, "Microsoft", "WindowsApps", "llama.exe")}
		}
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".local", "bin", "llama"), filepath.Join(home, ".llama-app", "llama")}
}
