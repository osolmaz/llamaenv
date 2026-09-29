//go:build darwin

package proc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// How often the watcher checks its parent, and how long the child's process
// group gets to stop before it is killed.
var (
	watchInterval = 250 * time.Millisecond
	watchGrace    = 5 * time.Second
)

// prepare starts the child under the watcher, in a new process group that
// the child and the servers it starts share with the watcher.
func prepare(cmd *exec.Cmd) {
	if Watcher != "" {
		cmd.Args = append([]string{Watcher, WatchCommand, strconv.Itoa(os.Getpid()), cmd.Path}, cmd.Args[1:]...)
		cmd.Path = Watcher
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// Watch runs "<parent pid> <program> <args...>" and returns the program's
// exit code. When the parent ends, also from a hard kill, it stops its
// process group: the program and every server that the program started.
func Watch(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "llamaenv: watch needs a parent pid and a program")
		return 2
	}
	parent, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "llamaenv: watch: bad parent pid:", args[0])
		return 2
	}
	// A signal to the group reaches the program directly. The watcher only
	// catches it, so that it stays until the program ends; a caught signal is
	// reset for the program, and an ignored one would not be.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	// G204: the program and arguments come from llamaenv's own prepare.
	cmd := exec.CommandContext(context.Background(), args[1], args[2:]...) //nolint:gosec // from prepare, see above
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "llamaenv:", err)
		return 127
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	tick := time.NewTicker(watchInterval)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return exitCode(cmd)
		case <-tick.C:
			if os.Getppid() != parent {
				stopOwnGroup(done)
				return 1
			}
		}
	}
}

// stopOwnGroup asks the watcher's process group to stop, and kills it,
// the watcher included, when the program has not ended in time.
func stopOwnGroup(done <-chan struct{}) {
	_ = syscall.Kill(0, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(watchGrace):
		_ = syscall.Kill(0, syscall.SIGKILL)
	}
}

func exitCode(cmd *exec.Cmd) int {
	if code := cmd.ProcessState.ExitCode(); code >= 0 {
		return code
	}
	return 1
}
