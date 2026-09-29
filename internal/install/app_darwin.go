//go:build darwin

package install

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The macOS Llama app's bundle identifier (ggml-org/Llama-macOS).
const llamaAppBundle = "app.llama.Llama"

// appCandidates is the list that the macOS Llama app checks, in its order
// (ggml-org/Llama-macOS, Llama/Engine/LlamaBinaries.swift): its own llama,
// which install.sh also writes, then the Homebrew bin folders.
func appCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".llama-app", "llama"), "/opt/homebrew/bin/llama", "/usr/local/bin/llama"}
}

func restartApp() (bool, error) {
	return macApp{run: output, poll: 200 * time.Millisecond, timeout: 30 * time.Second}.restart()
}

// macApp controls the Llama app through osascript and open.
type macApp struct {
	run     func(name string, args ...string) (string, error)
	poll    time.Duration
	timeout time.Duration
}

// restart quits the Llama app, which stops the server it started, and opens
// it again, so that it starts its server with the llama it finds now. It
// reports false when the app is not running.
func (m macApp) restart() (bool, error) {
	if !m.running() {
		return false, nil
	}
	if _, err := m.run("osascript", "-e", `tell application id "`+llamaAppBundle+`" to quit`); err != nil {
		return false, err
	}
	for deadline := time.Now().Add(m.timeout); m.running(); time.Sleep(m.poll) {
		if time.Now().After(deadline) {
			return false, errors.New("the Llama app did not quit")
		}
	}
	_, err := m.run("open", "-b", llamaAppBundle)
	return err == nil, err
}

func (m macApp) running() bool {
	out, err := m.run("osascript", "-e", `application id "`+llamaAppBundle+`" is running`)
	return err == nil && strings.TrimSpace(out) == "true"
}

// output runs osascript or open with llamaenv's fixed arguments.
func output(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// G204: only osascript and open, with scripts built from llamaAppBundle.
	out, err := exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // fixed commands, see above
	return string(out), err
}
