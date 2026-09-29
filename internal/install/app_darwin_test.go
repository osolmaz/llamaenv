//go:build darwin

package install

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeApp answers osascript and open like the Llama app would.
type fakeApp struct {
	running  []bool // the answers to "is running", in order; then false
	failOpen bool
	ran      []string
}

func (f *fakeApp) run(name string, args ...string) (string, error) {
	cmd := name + " " + strings.Join(args, " ")
	f.ran = append(f.ran, cmd)
	switch {
	case strings.HasSuffix(cmd, "is running"):
		up := len(f.running) > 0 && f.running[0]
		if len(f.running) > 0 {
			f.running = f.running[1:]
		}
		return map[bool]string{true: "true\n", false: "false\n"}[up], nil
	case name == "open" && f.failOpen:
		return "", errors.New("no app")
	}
	return "", nil
}

func (f *fakeApp) app() macApp {
	return macApp{run: f.run, poll: time.Millisecond, timeout: 50 * time.Millisecond}
}

func TestTheAppIsRestartedOnlyWhenItRuns(t *testing.T) {
	stopped := &fakeApp{}
	if ok, err := stopped.app().restart(); ok || err != nil || len(stopped.ran) != 1 {
		t.Errorf("restart of a stopped app = %v, %v; ran %q", ok, err, stopped.ran)
	}
	f := &fakeApp{running: []bool{true, true, true}} // quits after two checks
	if ok, err := f.app().restart(); !ok || err != nil {
		t.Fatalf("restart = %v, %v", ok, err)
	}
	if last := f.ran[len(f.ran)-1]; last != "open -b "+llamaAppBundle {
		t.Errorf("last command %q, want open", last)
	}
}

func TestAnAppThatDoesNotQuitIsReported(t *testing.T) {
	stuck := &fakeApp{running: make([]bool, 1000)}
	for i := range stuck.running {
		stuck.running[i] = true
	}
	if ok, err := stuck.app().restart(); ok || err == nil {
		t.Errorf("restart = %v, %v; want an error", ok, err)
	}
	noOpen := &fakeApp{running: []bool{true}, failOpen: true}
	if ok, err := noOpen.app().restart(); ok || err == nil {
		t.Errorf("restart with a failing open = %v, %v", ok, err)
	}
}
