//go:build linux

package proc

import (
	"os/exec"
	"syscall"
)

// prepare gives the child its own process group, so a signal to the group
// also reaches the servers it starts, and makes it die with llamaenv.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}

// Watch is only needed on macOS.
func Watch([]string) int { return 2 }
