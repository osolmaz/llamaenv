// Package cli implements the llamaenv command.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/osolmaz/llamaenv/internal/config"
	"github.com/osolmaz/llamaenv/internal/install"
	"github.com/osolmaz/llamaenv/internal/runtimes"
	"github.com/osolmaz/llamaenv/internal/shim"
	"github.com/osolmaz/llamaenv/internal/switcher"
)

// Version is set at build time.
var Version = "dev"

const usage = `llamaenv: run each model with the llama.cpp runtime it needs

Work in progress. llamaenv is a stopgap until llama.cpp can do this itself.

Usage: llamaenv <command> [arguments]

Setup:
  install [--skip-official]   put the llama shim first on the user PATH
  uninstall                   remove llamaenv; the standard llama stays

Runtimes:
  runtime add <name> <folder | archive URL...>
  runtime remove <name>
  runtime list
  use <runtime | official>    default runtime for models without a mapping
  versions                    versions of the official llama and the runtimes

Models:
  map <repo[:quant]> <runtime>
  unmap <repo[:quant]>
  list                        mappings and the runtime each one uses

Presets (plain llama.cpp presets, used only by that runtime's router):
  preset add <runtime> <file.ini | URL>   one per model, kept under its file name
  preset remove <runtime> <file.ini>

Diagnosis:
  status                      the running switcher and its routers
  serve [llama serve args]    run the switcher in the foreground
  version
`

// say writes command output. A failed write to the terminal cannot be
// reported anywhere better, so it is ignored.
func say(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }

// Main runs llamaenv and returns the exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		say(stdout, "%s", usage)
		return 0
	}
	if err := run(args, stdout); err != nil {
		say(stderr, "llamaenv: %v\n", err)
		return 1
	}
	return 0
}

type setupCommand func(d config.Dirs, args []string, out io.Writer) error
type configCommand func(c *config.Config, args []string, out io.Writer) error

var setupCommands = map[string]setupCommand{
	"install": func(d config.Dirs, args []string, out io.Writer) error {
		return install.Install(d, install.Options{SkipOfficial: has(args, "--skip-official"), Log: logTo(out)})
	},
	"uninstall": func(d config.Dirs, _ []string, out io.Writer) error { return install.Uninstall(d, logTo(out)) },
	"serve":     serve,
	"status":    status,
}

var configCommands = map[string]configCommand{
	"runtime":  runtimeCmd,
	"use":      use,
	"versions": versions,
	"map":      mapModel,
	"unmap":    unmapModel,
	"list":     list,
	"preset":   preset,
}

func run(args []string, out io.Writer) error {
	name, rest := args[0], args[1:]
	if name == "version" {
		say(out, "llamaenv %s\n", Version)
		return nil
	}
	dirs, err := config.DefaultDirs()
	if err != nil {
		return err
	}
	if cmd, ok := setupCommands[name]; ok {
		return cmd(dirs, rest, out)
	}
	cmd, ok := configCommands[name]
	if !ok {
		return fmt.Errorf("unknown command %q; run 'llamaenv help'", name)
	}
	c, err := config.Load(dirs)
	if err != nil {
		return err
	}
	return cmd(c, rest, out)
}

func logTo(out io.Writer) func(string) { return func(s string) { say(out, "%s\n", s) } }

func serve(_ config.Dirs, args []string, _ io.Writer) error {
	real, err := shim.RealLlama()
	if err != nil {
		return err
	}
	if code := shim.Serve(real, append([]string{"serve"}, args...)); code != 0 {
		return fmt.Errorf("serve exited with code %d", code)
	}
	return nil
}

func mapModel(c *config.Config, args []string, out io.Writer) error {
	if len(args) != 2 {
		return errors.New("usage: llamaenv map <repo[:quant]> <runtime>")
	}
	model, rt := args[0], args[1]
	if rt != config.Official && c.Runtime(rt) == nil {
		return fmt.Errorf("unknown runtime %q; add it with 'llamaenv runtime add' first", rt)
	}
	c.Map(model, rt)
	if err := c.Save(); err != nil {
		return err
	}
	say(out, "%s now runs on %s (restart the llama server to apply)\n", model, rt)
	return nil
}

func unmapModel(c *config.Config, args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: llamaenv unmap <repo[:quant]>")
	}
	if !c.Unmap(args[0]) {
		return fmt.Errorf("%s has no mapping", args[0])
	}
	if err := c.Save(); err != nil {
		return err
	}
	say(out, "%s now runs on the default runtime\n", args[0])
	return nil
}

var runtimeCommands = map[string]configCommand{
	"add":    runtimeAdd,
	"remove": runtimeRemove,
	"list":   runtimeList,
}

func runtimeCmd(c *config.Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: llamaenv runtime add|remove|list")
	}
	cmd, ok := runtimeCommands[args[0]]
	if !ok {
		return fmt.Errorf("unknown runtime command %q", args[0])
	}
	return cmd(c, args[1:], out)
}

func runtimeAdd(c *config.Config, args []string, out io.Writer) error {
	if len(args) < 2 {
		return errors.New("usage: llamaenv runtime add <name> <folder | archive URL...>")
	}
	rt, err := runtimes.Add(c, args[0], args[1:], logTo(out))
	if err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	say(out, "added runtime %s in %s\n", rt.Name, rt.Dir)
	return nil
}

func runtimeRemove(c *config.Config, args []string, _ io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: llamaenv runtime remove <name>")
	}
	name := args[0]
	for _, m := range c.Mappings() {
		if strings.EqualFold(m.Runtime, name) {
			return fmt.Errorf("%s is mapped to %s; unmap it first", m.Model, name)
		}
	}
	if c.Default() == name {
		return fmt.Errorf("%s is the default runtime; run 'llamaenv use official' first", name)
	}
	if err := runtimes.Remove(c, name); err != nil {
		return err
	}
	return c.Save()
}

func runtimeList(c *config.Config, _ []string, out io.Writer) error {
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	say(tw, "RUNTIME\tPROGRAM\tSTATE\n")
	real, err := shim.RealLlama()
	say(tw, "%s\t%s\t%s\n", config.Official, real, errText(err))
	for _, n := range runtimes.Names(c) {
		rt, err := runtimes.Resolve(c, n)
		prog, _ := rt.Launch()
		say(tw, "%s\t%s\t%s\n", n, prog, errText(err))
	}
	return tw.Flush()
}

func use(c *config.Config, args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: llamaenv use <runtime | official>")
	}
	if args[0] != config.Official {
		rt, err := runtimes.Resolve(c, args[0])
		if err != nil {
			return err
		}
		if !rt.CanBeDefault() {
			return fmt.Errorf("%s has no %s, so it cannot serve every model; map single models to it instead", args[0], runtimes.Exe("llama"))
		}
	}
	c.Settings.Set("", "default", args[0])
	if err := c.Save(); err != nil {
		return err
	}
	say(out, "default runtime: %s (restart the llama server to apply)\n", args[0])
	return nil
}

func versions(c *config.Config, _ []string, out io.Writer) error {
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	say(tw, "RUNTIME\tVERSION\n")
	if real, err := shim.RealLlama(); err == nil {
		say(tw, "%s\t%s\n", config.Official, programVersion(real))
	} else {
		say(tw, "%s\t%v\n", config.Official, err)
	}
	for _, n := range runtimes.Names(c) {
		rt, err := runtimes.Resolve(c, n)
		if err != nil {
			say(tw, "%s\t%v\n", n, err)
			continue
		}
		prog, _ := rt.Launch()
		say(tw, "%s\t%s\n", n, programVersion(prog))
	}
	return tw.Flush()
}

// programVersion returns the "version:" line that llama.cpp programs print.
func programVersion(prog string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b, _ := exec.CommandContext(ctx, prog, "--version").CombinedOutput()
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "version:") {
			return strings.TrimSpace(l)
		}
	}
	if lines[0] != "" {
		return lines[0]
	}
	return "unknown"
}

func list(c *config.Config, _ []string, out io.Writer) error {
	say(out, "default runtime: %s\n\n", c.Default())
	m := c.Mappings()
	if len(m) == 0 {
		say(out, "no model mappings: every model runs on the default runtime\n")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	say(tw, "MODEL\tRUNTIME\tPRESET\tSTATE\n")
	for _, e := range m {
		state := "ready"
		if e.Runtime != config.Official {
			if _, err := runtimes.Resolve(c, e.Runtime); err != nil {
				state = err.Error()
			}
		}
		say(tw, "%s\t%s\t%s\t%s\n", e.Model, e.Runtime, orNone(strings.Join(c.PresetNames(e.Runtime), ", ")), state)
	}
	return tw.Flush()
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func preset(c *config.Config, args []string, out io.Writer) error {
	if len(args) != 3 || (args[0] != "add" && args[0] != "remove") {
		return errors.New("usage: llamaenv preset add <runtime> <file.ini | URL>, or llamaenv preset remove <runtime> <file.ini>")
	}
	if args[0] == "remove" {
		return removePreset(c, args[1], args[2], out)
	}
	runtime := args[1]
	dst, err := runtimes.AddPreset(c, runtime, args[2], logTo(out))
	if err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	say(out, "%s now uses the preset %s (restart the llama server to apply)\n", runtime, dst)
	if c.Default() == runtime {
		say(out, "note: %s is the default runtime, which serves every model, so it does not use presets\n", runtime)
	}
	return nil
}

func removePreset(c *config.Config, runtime, name string, out io.Writer) error {
	if err := runtimes.RemovePreset(c, runtime, name); err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	say(out, "removed the preset %s of %s (restart the llama server to apply)\n", name, runtime)
	return nil
}

func status(d config.Dirs, _ []string, out io.Writer) error {
	states := switcher.ReadStates(d.State())
	if len(states) == 0 {
		say(out, "no switcher is running\n")
		return nil
	}
	for i, st := range states {
		if i > 0 {
			say(out, "\n")
		}
		if err := printState(out, st); err != nil {
			return err
		}
	}
	return nil
}

func printState(out io.Writer, st switcher.State) error {
	say(out, "switcher pid %d on http://%s\n", st.PID, st.Address)
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	say(tw, "RUNTIME\tPORT\tPROGRAM\tSTATE\n")
	for _, b := range st.Backends {
		state := "running"
		if b.Error != "" {
			state = b.Error
		}
		say(tw, "%s\t%d\t%s\t%s\n", b.Name, b.Port, b.Program, state)
	}
	return tw.Flush()
}

func has(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func errText(err error) string {
	if err != nil {
		return err.Error()
	}
	return "ready"
}
