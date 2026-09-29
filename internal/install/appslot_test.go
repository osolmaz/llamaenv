//go:build !windows

package install

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeMac is a temporary copy of the places that the macOS Llama app checks:
// its own llama, a Homebrew bin folder, and /usr/local/bin.
type fakeMac struct {
	t        *testing.T
	root     string
	managed  string
	brewBin  string
	localBin string
	ran      []string
	failSudo string // a sudo command that starts with this fails
	slot     appSlot
}

func newFakeMac(t *testing.T) *fakeMac {
	t.Helper()
	root := t.TempDir()
	m := &fakeMac{
		t:        t,
		root:     root,
		managed:  filepath.Join(root, "home", ".llama-app", "llama"),
		brewBin:  filepath.Join(root, "homebrew", "bin", "llama"),
		localBin: filepath.Join(root, "local", "bin", "llama"),
	}
	for _, dir := range []string{filepath.Dir(m.managed), filepath.Dir(m.brewBin), filepath.Dir(m.localBin), filepath.Join(root, "llamaenv", "bin")} {
		must(t, os.MkdirAll(dir, 0o750))
	}
	shim := filepath.Join(root, "llamaenv", "bin", "llama")
	writeFile(t, shim, "shim")
	m.slot = appSlot{
		candidates: []string{m.managed, m.brewBin, m.localBin},
		shim:       shim,
		dir:        filepath.Join(root, "llamaenv", "official"),
		run:        m.run,
	}
	return m
}

// run fakes brew and sudo: brew links and unlinks the formula, and sudo runs
// its command after it lifts the read-only mode of the folders.
func (m *fakeMac) run(name string, args ...string) error {
	m.ran = append(m.ran, filepath.Base(name)+" "+strings.Join(args, " "))
	switch {
	case filepath.Base(name) == "brew" && args[0] == "unlink":
		return os.Remove(m.brewBin)
	case filepath.Base(name) == "brew" && args[0] == "link":
		return os.Symlink("../Cellar/llama.cpp/1/bin/llama", m.brewBin)
	case name == "sudo":
		if m.failSudo != "" && strings.HasPrefix(strings.Join(args, " "), m.failSudo) {
			return errors.New("sudo: a password is required")
		}
		// As root: the folder stays read-only for everything else.
		dir := filepath.Dir(m.localBin)
		must(m.t, os.Chmod(dir, 0o750))                    //nolint:gosec // a test folder
		defer func() { must(m.t, os.Chmod(dir, 0o500)) }() //nolint:gosec // a test folder
		return exec.Command(args[0], args[1:]...).Run()    //nolint:gosec,noctx // the test's own command
	}
	return nil
}

// homebrew installs a fake llama.cpp formula, linked into the bin folder.
func (m *fakeMac) homebrew() {
	prefix := filepath.Dir(filepath.Dir(m.brewBin))
	writeFile(m.t, filepath.Join(prefix, "Cellar", "llama.cpp", "1", "bin", "llama"), "brew")
	must(m.t, os.MkdirAll(filepath.Join(prefix, "opt"), 0o750))
	must(m.t, os.Symlink("../Cellar/llama.cpp/1", filepath.Join(prefix, "opt", "llama.cpp")))
	must(m.t, os.Symlink("../Cellar/llama.cpp/1/bin/llama", m.brewBin))
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o750))
	must(t, os.WriteFile(path, []byte(text), 0o700)) //nolint:gosec // a fake program
}

func (m *fakeMac) take() {
	m.t.Helper()
	must(m.t, m.slot.take(func(string) {}))
}

func (m *fakeMac) release() {
	m.t.Helper()
	must(m.t, m.slot.release(func(string) {}))
}

func TestTheAppsOwnLlamaIsKeptAndPutBack(t *testing.T) {
	m := newFakeMac(t)
	writeFile(t, m.managed, "official")
	m.take()
	if !m.slot.isShim(m.managed) {
		t.Fatal("the app's llama is not the shim")
	}
	if got := read(t, m.slot.official()); got != "official" {
		t.Errorf("kept official llama = %q", got)
	}
	if s := m.slot.status(); !strings.Contains(s, "runs llamaenv's llama at "+m.managed) {
		t.Errorf("status = %q", s)
	}
	m.take() // taking it again changes nothing
	if got := read(t, m.slot.official()); got != "official" {
		t.Errorf("after a second take, kept official llama = %q", got)
	}
	m.release()
	if st, err := os.Lstat(m.managed); err != nil || st.Mode()&os.ModeSymlink != 0 || read(t, m.managed) != "official" {
		t.Errorf("the app's llama was not put back: %v", err)
	}
	if len(m.ran) != 0 {
		t.Errorf("ran %q, want nothing", m.ran)
	}
}

func TestAnUpdateOfTheAppsLlamaTurnsLlamaenvOffSafely(t *testing.T) {
	m := newFakeMac(t)
	writeFile(t, m.managed, "old")
	m.take()
	// The app promotes its staged update over the shim.
	must(t, os.Remove(m.managed))
	writeFile(t, m.managed, "new")
	if s := m.slot.status(); !strings.Contains(s, "without llamaenv") || !strings.Contains(s, "llamaenv install") {
		t.Errorf("status = %q", s)
	}
	m.take() // install again keeps the new official llama
	if got := read(t, m.slot.official()); got != "new" {
		t.Errorf("kept official llama = %q, want the update", got)
	}
	must(t, os.Remove(m.managed))
	writeFile(t, m.managed, "newer")
	m.release() // uninstall leaves the app's newer llama as it is
	if got := read(t, m.managed); got != "newer" {
		t.Errorf("the app's llama = %q, want the newer one", got)
	}
}

func TestHomebrewsLlamaIsUnlinkedAndLinkedAgain(t *testing.T) {
	m := newFakeMac(t)
	m.homebrew()
	m.take()
	if !m.slot.isShim(m.localBin) || exists(m.brewBin) {
		t.Fatal("the shim is not the next place in the app's list")
	}
	if got := read(t, m.slot.official()); got != "brew" {
		t.Errorf("official llama = %q, want Homebrew's", got)
	}
	m.release()
	if exists(m.localBin) || read(t, m.brewBin) != "brew" {
		t.Error("Homebrew's llama was not linked again")
	}
	want := []string{"brew unlink llama.cpp", "brew link llama.cpp"}
	if strings.Join(m.ran, ";") != strings.Join(want, ";") {
		t.Errorf("ran %q, want %q", m.ran, want)
	}
}

func TestABrewUpgradeTurnsLlamaenvOffAndInstallTakesItAgain(t *testing.T) {
	m := newFakeMac(t)
	m.homebrew()
	m.take()
	must(t, m.run("brew", "link", "llama.cpp")) // brew upgrade links the formula again
	if s := m.slot.status(); !strings.Contains(s, "runs "+m.brewBin+" without llamaenv") {
		t.Errorf("status = %q", s)
	}
	m.take()
	if !m.slot.isShim(m.localBin) || exists(m.brewBin) {
		t.Error("install did not take the app's llama again")
	}
	must(t, m.run("brew", "link", "llama.cpp"))
	m.ran = nil
	m.release()
	if len(m.ran) != 0 {
		t.Errorf("ran %q; Homebrew was already linked", m.ran)
	}
}

func TestHomebrewNeedsAFreePlaceAfterIt(t *testing.T) {
	m := newFakeMac(t)
	m.homebrew()
	writeFile(t, m.localBin, "manual build")
	if err := m.slot.take(func(string) {}); err == nil || !strings.Contains(err.Error(), "no later place") {
		t.Errorf("take = %v", err)
	}
	if len(m.ran) != 0 || !exists(m.brewBin) {
		t.Errorf("changed Homebrew: ran %q", m.ran)
	}
}

func TestASymlinkedLlamaIsPutBackAsTheSameSymlink(t *testing.T) {
	m := newFakeMac(t)
	build := filepath.Join(m.root, "build", "bin", "llama")
	writeFile(t, build, "source build")
	must(t, os.Symlink("../../build/bin/llama", m.localBin))
	m.take()
	if got := read(t, m.slot.official()); got != "source build" {
		t.Errorf("official llama = %q", got)
	}
	m.release()
	if link, err := os.Readlink(m.localBin); err != nil || link != "../../build/bin/llama" {
		t.Errorf("link = %q, %v", link, err)
	}
}

func TestAProtectedFolderIsChangedThroughSudo(t *testing.T) {
	m := newFakeMac(t)
	writeFile(t, m.localBin, "manual build")
	must(t, os.Chmod(filepath.Dir(m.localBin), 0o500))                  //nolint:gosec // a test folder
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(m.localBin), 0o750) }) //nolint:gosec // a test folder
	m.take()
	if !m.slot.isShim(m.localBin) || read(t, m.slot.official()) != "manual build" {
		t.Error("the shim did not take the protected place")
	}
	if len(m.ran) != 2 || !strings.HasPrefix(m.ran[0], "sudo mv -f") || !strings.HasPrefix(m.ran[1], "sudo ln -s") {
		t.Errorf("ran %q, want sudo mv and sudo ln", m.ran)
	}
	m.release()
	if read(t, m.localBin) != "manual build" {
		t.Error("the protected llama was not put back")
	}
}

func TestAFailedShimStepPutsTheOfficialLlamaBack(t *testing.T) {
	m := newFakeMac(t)
	writeFile(t, m.localBin, "manual build")
	must(t, os.Chmod(filepath.Dir(m.localBin), 0o500))                  //nolint:gosec // a test folder
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(m.localBin), 0o750) }) //nolint:gosec // a test folder
	m.failSudo = "ln"
	if err := m.slot.take(func(string) {}); err == nil {
		t.Fatal("take did not fail")
	}
	if read(t, m.localBin) != "manual build" {
		t.Error("the official llama was not put back after the failed step")
	}
}

func TestAFailedRecordChangesNothing(t *testing.T) {
	m := newFakeMac(t)
	writeFile(t, m.managed, "official")
	must(t, os.MkdirAll(m.slot.dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(m.slot.dir, 0o750) }) //nolint:gosec // a test folder
	if err := m.slot.take(func(string) {}); err == nil {
		t.Fatal("take did not fail")
	}
	if st, err := os.Lstat(m.managed); err != nil || st.Mode()&os.ModeSymlink != 0 {
		t.Errorf("the app's llama changed: %v", err)
	}
}

func TestUninstallFinishesAnInterruptedInstall(t *testing.T) {
	m := newFakeMac(t)
	writeFile(t, m.managed, "official")
	must(t, os.MkdirAll(m.slot.dir, 0o750))
	// An install that stopped after it moved the official llama away.
	must(t, writeRecord(m.slot.record(), slotRecord{Slot: m.managed}))
	must(t, os.Rename(m.managed, m.slot.official()))
	m.release()
	if read(t, m.managed) != "official" {
		t.Error("the official llama was not put back")
	}
}

func TestACurlInstallAfterHomebrewUndoesTheHomebrewChange(t *testing.T) {
	m := newFakeMac(t)
	m.homebrew()
	m.take()
	writeFile(t, m.managed, "curl") // install.sh runs; the app now prefers it
	m.take()
	if !m.slot.isShim(m.managed) || exists(m.localBin) || read(t, m.brewBin) != "brew" {
		t.Error("the Homebrew change was not undone before the new take")
	}
	m.release()
	if read(t, m.managed) != "curl" || read(t, m.brewBin) != "brew" {
		t.Error("uninstall did not restore both installs")
	}
}

func TestNoLlamaAtAllIsAnError(t *testing.T) {
	m := newFakeMac(t)
	if err := m.slot.take(func(string) {}); err == nil || !strings.Contains(err.Error(), "finds no llama") {
		t.Errorf("take = %v", err)
	}
	if s := m.slot.status(); s != "Llama app: finds no llama" {
		t.Errorf("status = %q", s)
	}
}

func TestOffMacOSNothingIsTaken(t *testing.T) {
	a := appSlot{dir: t.TempDir()}
	if err := a.take(func(string) {}); err != nil || a.status() != "" {
		t.Errorf("take = %v, status = %q", err, a.status())
	}
	if err := a.release(func(string) {}); err != nil {
		t.Error(err)
	}
}
