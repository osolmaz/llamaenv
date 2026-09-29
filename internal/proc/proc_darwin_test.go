//go:build darwin

package proc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == WatchCommand {
		os.Exit(Watch(os.Args[2:]))
	}
	switch os.Getenv("PROC_TEST_MODE") {
	case "exit":
		code, _ := strconv.Atoi(os.Getenv("PROC_TEST_EXIT"))
		os.Exit(code)
	case "sleep":
		_ = os.WriteFile(filepath.Clean(os.Getenv("PROC_TEST_PIDFILE")), []byte(strconv.Itoa(os.Getpid())), 0o600)
		time.Sleep(time.Minute)
		os.Exit(0)
	case "parent":
		Watcher = self()
		g, err := NewGroup()
		if err != nil {
			os.Exit(3)
		}
		child := testProgram()
		child.Env = append(os.Environ(), "PROC_TEST_MODE=sleep")
		if g.Start(child) != nil {
			os.Exit(4)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestTheWatcherPassesOnTheExitCode(t *testing.T) {
	Watcher = self()
	t.Cleanup(func() { Watcher = "" })
	g, err := NewGroup()
	if err != nil {
		t.Fatal(err)
	}
	cmd := testProgram()
	cmd.Env = append(os.Environ(), "PROC_TEST_MODE=exit", "PROC_TEST_EXIT=5")
	if err := g.Start(cmd); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exit) || exit.ExitCode() != 5 {
		t.Fatalf("exit %v, want 5", err)
	}
	if cmd.Args[1] != WatchCommand {
		t.Errorf("the child did not run under the watcher: %q", cmd.Args)
	}
}

func TestAChildStopsWhenItsParentIsKilled(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	parent := testProgram()
	parent.Env = append(os.Environ(), "PROC_TEST_MODE=parent", "PROC_TEST_PIDFILE="+pidFile)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	child := waitForPid(t, pidFile)
	// A hard kill, as the Llama app does 2 seconds after it asks its server to stop.
	_ = parent.Process.Kill()
	_ = parent.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(child, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(child, syscall.SIGKILL)
	t.Fatalf("child %d still runs after its parent was killed", child)
}

func TestWatchRejectsBadArguments(t *testing.T) {
	if Watch(nil) != 2 || Watch([]string{"x", "prog"}) != 2 {
		t.Error("bad arguments did not return 2")
	}
}

func waitForPid(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(filepath.Clean(file)); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the child did not start")
	return 0
}

// self is the test binary, which acts as every program in these tests.
func self() string {
	p, _ := os.Executable()
	return p
}

// testProgram runs the test binary.
func testProgram() *exec.Cmd {
	// G204: the test binary itself.
	return exec.CommandContext(context.Background(), self()) //nolint:gosec // see above
}
