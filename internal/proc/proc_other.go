//go:build !linux && !windows && !darwin

package proc

import "os/exec"

// Other systems are not supported targets; this keeps the code building there
// for development, with plain child processes.
type sysGroup struct{}

func newSysGroup() (sysGroup, error)      { return sysGroup{}, nil }
func prepare(*exec.Cmd)                   {}
func (sysGroup) add(*exec.Cmd) error      { return nil }
func terminate(c *exec.Cmd)               { _ = c.Process.Kill() }
func alive(c *exec.Cmd) bool              { return c.Process.Signal(nil) == nil }
func (sysGroup) killAll(cmds []*exec.Cmd) {}

// Watch is only needed on macOS.
func Watch([]string) int { return 2 }
