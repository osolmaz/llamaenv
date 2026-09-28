//go:build linux

package proc

import (
	"os/exec"
	"syscall"
)

type sysGroup struct{}

func newSysGroup() (sysGroup, error) { return sysGroup{}, nil }

// prepare gives the child its own process group, so a signal to the group
// also reaches the servers it starts, and makes it die with llamaenv.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}

func (sysGroup) add(*exec.Cmd) error { return nil }

func terminate(c *exec.Cmd) {
	if c.Process != nil {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
	}
}

func alive(c *exec.Cmd) bool { return syscall.Kill(c.Process.Pid, 0) == nil }

func (sysGroup) killAll(cmds []*exec.Cmd) {
	for _, c := range cmds {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
	}
}
