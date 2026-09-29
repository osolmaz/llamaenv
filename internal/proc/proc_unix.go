//go:build linux || darwin

package proc

import (
	"os"
	"os/exec"
	"syscall"
)

type sysGroup struct{}

func newSysGroup() (sysGroup, error) { return sysGroup{}, nil }

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

func signalGroup(c *exec.Cmd, s os.Signal) {
	if sig, ok := s.(syscall.Signal); ok {
		_ = syscall.Kill(-c.Process.Pid, sig)
		return
	}
	_ = c.Process.Signal(s)
}
