// Package proc starts child processes that stop together with llamaenv.
//
// On Windows every child goes into a job object that kills its processes
// when llamaenv's handle closes, so a hard stop of the shim (the Llama app
// stops its server that way) also stops every backend. On Linux every child
// gets its own process group and dies with its parent. macOS has no way to
// make a child die with its parent, so there every child runs under a small
// watcher, llamaenv's own program, that stops the child's process group when
// the parent ends.
package proc

import (
	"os/exec"
	"sync"
	"time"
)

// WatchCommand is the first argument that makes llamaenv's program run
// Watch. Only macOS uses it.
const WatchCommand = "__llamaenv-watch"

// Watcher is the program that runs Watch: llamaenv's own program, which main
// sets. When it is empty, children start as plain processes.
var Watcher string

// Group is a set of child processes.
type Group struct {
	mu    sync.Mutex
	cmds  []*exec.Cmd
	sys   sysGroup
	close sync.Once
}

// NewGroup creates an empty group.
func NewGroup() (*Group, error) {
	sys, err := newSysGroup()
	if err != nil {
		return nil, err
	}
	return &Group{sys: sys}, nil
}

// Start starts cmd as a member of the group.
func (g *Group) Start(cmd *exec.Cmd) error {
	prepare(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	g.mu.Lock()
	g.cmds = append(g.cmds, cmd)
	g.mu.Unlock()
	return g.sys.add(cmd)
}

// Stop asks every child to stop, waits up to timeout, then kills the rest.
func (g *Group) Stop(timeout time.Duration) {
	g.close.Do(func() {
		g.mu.Lock()
		cmds := append([]*exec.Cmd(nil), g.cmds...)
		g.mu.Unlock()
		for _, c := range cmds {
			terminate(c)
		}
		deadline := time.Now().Add(timeout)
		for _, c := range cmds {
			for time.Now().Before(deadline) && running(c) {
				time.Sleep(50 * time.Millisecond)
			}
		}
		g.sys.killAll(cmds)
	})
}

func running(c *exec.Cmd) bool { return c.Process != nil && alive(c) }
