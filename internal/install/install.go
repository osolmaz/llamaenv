// Package install adds llamaenv next to the standard llama install, and
// removes it again without leftovers.
package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/osolmaz/llamaenv/internal/config"
	"github.com/osolmaz/llamaenv/internal/runtimes"
	"github.com/osolmaz/llamaenv/internal/shim"
	"github.com/osolmaz/llamaenv/internal/switcher"
)

// Options for Install.
type Options struct {
	// SkipOfficial skips installing the official llama when it is missing.
	SkipOfficial bool
	Log          func(string)
}

// Install makes sure the official llama exists, installs the shim and
// llamaenv into llamaenv's bin folder, puts that folder first on the user
// PATH, and restarts the Llama app so that it picks up the shim.
func Install(d config.Dirs, o Options) error {
	if err := ensureOfficial(o); err != nil {
		return err
	}
	if err := installPrograms(d); err != nil {
		return err
	}
	if err := addToPath(d.Bin()); err != nil {
		return fmt.Errorf("add %s to PATH: %w", d.Bin(), err)
	}
	o.Log("added " + d.Bin() + " to the front of the user PATH")
	reportRestart(o.Log)
	return nil
}

// ensureOfficial installs the official llama through llama.app's own
// script when it is missing, unless the caller said not to.
func ensureOfficial(o Options) error {
	if _, err := shim.RealLlama(); err == nil {
		return nil
	} else if o.SkipOfficial {
		return err
	}
	o.Log("installing the official llama from llama.app")
	if err := installOfficial(); err != nil {
		return fmt.Errorf("install the official llama: %w", err)
	}
	_, err := shim.RealLlama()
	return err
}

// installPrograms copies this program into the bin folder twice: as the
// llama shim and as llamaenv.
func installPrograms(d config.Dirs) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.Bin(), 0o750); err != nil {
		return err
	}
	for _, name := range []string{runtimes.Exe("llama"), runtimes.Exe("llamaenv")} {
		if err := copyFile(self, filepath.Join(d.Bin(), name)); err != nil {
			return err
		}
	}
	return nil
}

func reportRestart(log func(string)) {
	restarted, err := restartApp()
	switch {
	case err != nil:
		log("could not restart the Llama app, restart it yourself: " + err.Error())
	case restarted:
		log("restarted the Llama app")
	}
}

// Uninstall stops the switcher, removes llamaenv's PATH entry and folders,
// and restarts the Llama app, which then runs the official llama again.
// Model files in the Hugging Face cache stay.
func Uninstall(d config.Dirs, log func(string)) error {
	stopSwitcher(d, log)
	if err := removeFromPath(d.Bin()); err != nil {
		return fmt.Errorf("remove %s from PATH: %w", d.Bin(), err)
	}
	log("removed " + d.Bin() + " from the user PATH")
	reportRestart(log)
	if err := os.RemoveAll(d.Config); err != nil {
		return err
	}
	if err := removeDataDir(d.Data); err != nil {
		return err
	}
	log("removed llamaenv's files; model files in the Hugging Face cache stay")
	return nil
}

func stopSwitcher(d config.Dirs, log func(string)) {
	data, err := os.ReadFile(filepath.Join(d.State(), "switcher.json"))
	if err != nil {
		return
	}
	var st switcher.State
	if json.Unmarshal(data, &st) != nil || st.PID == 0 || st.PID == os.Getpid() {
		return
	}
	if p, err := os.FindProcess(st.PID); err == nil {
		if p.Kill() == nil {
			log(fmt.Sprintf("stopped the running switcher (pid %d)", st.PID))
		}
	}
}

func copyFile(src, dst string) error {
	if same(src, dst) {
		return nil
	}
	tmp := dst + ".new"
	if err := writeProgram(src, tmp); err != nil {
		return err
	}
	if err := replaceFile(tmp, dst); err != nil {
		return fmt.Errorf("replace %s (is a server using it? stop it first): %w", dst, err)
	}
	return nil
}

// writeProgram copies src to a new program file at dst.
func writeProgram(src, dst string) error {
	in, err := os.Open(filepath.Clean(src))
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(filepath.Clean(dst), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	// G302: the shim and llamaenv are programs, executable by their owner only.
	return os.Chmod(dst, 0o700) //nolint:gosec // owner-only program, see above
}

func same(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

func installOfficial() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cmd, err := officialInstaller(ctx, runtime.GOOS, exec.LookPath)
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

// officialInstaller builds the command for llama.app's own install script.
func officialInstaller(ctx context.Context, goos string, lookPath func(string) (string, error)) (*exec.Cmd, error) {
	if goos == "windows" {
		return exec.CommandContext(ctx, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", "irm https://llama.app/install.ps1 | iex"), nil
	}
	if _, err := lookPath("curl"); err != nil {
		return nil, errors.New("curl is needed to install the official llama")
	}
	return exec.CommandContext(ctx, "sh", "-c", "curl -LsSf https://llama.app/install.sh | sh"), nil
}
