package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// appSlot takes the place of the llama that the macOS Llama app runs. The app
// does not use PATH: it runs the first program of a fixed list (candidates),
// so llamaenv puts a symlink to its shim at the file that the app runs now,
// and keeps the official llama that was there in its own folder. Uninstall
// puts that llama back. See "Platform exceptions" in DESIGN_PRINCIPLES.md.
//
// A Homebrew llama is left in its keg: llamaenv unlinks the formula, keeps a
// symlink to the keg's stable opt path, and takes the next free place in the
// list, because Homebrew owns the symlink in its bin folder.
type appSlot struct {
	candidates []string // in the order that the app checks them; none off macOS
	shim       string   // llamaenv's llama
	dir        string   // llamaenv's folder for the official llama and the record
	// run runs a command, for Homebrew and for sudo.
	run func(name string, args ...string) error
}

// slotRecord says what llamaenv changed, so that uninstall can undo exactly that.
type slotRecord struct {
	Slot string `json:"slot"` // the file that now links to the shim
	// Link is the text of the symlink that was at Slot; empty when Slot held a
	// program file, which llamaenv moved to its folder.
	Link string `json:"link,omitempty"`
	// Brew and Formula name the Homebrew formula that llamaenv unlinked.
	Brew    string `json:"brew,omitempty"`
	Formula string `json:"formula,omitempty"`
}

func (a appSlot) official() string { return filepath.Join(a.dir, "llama") }
func (a appSlot) record() string   { return filepath.Join(a.dir, "slot.json") }

// current returns the index of the program that the app runs now, or -1.
func (a appSlot) current() int {
	for i, c := range a.candidates {
		if isProgram(c) {
			return i
		}
	}
	return -1
}

// isShim says whether path is a symlink to llamaenv's shim.
func (a appSlot) isShim(path string) bool {
	target, err := os.Readlink(path)
	return err == nil && filepath.Clean(target) == filepath.Clean(a.shim)
}

// take makes the app run the shim. Taking it again is safe: after the app or
// an installer replaced the shim, it takes the new official llama instead.
func (a appSlot) take(log func(string)) error {
	if len(a.candidates) == 0 {
		return nil
	}
	i := a.current()
	switch {
	case i < 0:
		return errors.New("the Llama app finds no llama at " + strings.Join(a.candidates, ", "))
	case a.isShim(a.candidates[i]):
		log("the Llama app already runs llamaenv's llama at " + a.candidates[i])
		return nil
	}
	if err := os.MkdirAll(a.dir, 0o750); err != nil {
		return err
	}
	if formula, prefix, ok := homebrew(a.candidates[i]); ok {
		return a.takeFromHomebrew(i, formula, prefix, log)
	}
	return a.takeFile(a.candidates[i], log)
}

// takeFile keeps the official llama at slot in llamaenv's folder and puts
// the shim in its place.
func (a appSlot) takeFile(slot string, log func(string)) error {
	link, err := a.keepOfficial(slot)
	if err != nil {
		return fmt.Errorf("keep the official llama: %w", err)
	}
	if err := a.linkShim(slot); err != nil {
		return err
	}
	log("the Llama app now runs llamaenv's llama at " + slot + "; the official llama it ran is kept in " + a.dir)
	return writeRecord(a.record(), slotRecord{Slot: slot, Link: link})
}

// keepOfficial moves the program at slot to llamaenv's folder. A symlink
// stays where it points, and its text is returned, so that uninstall can
// restore it exactly.
func (a appSlot) keepOfficial(slot string) (string, error) {
	link, err := os.Readlink(slot)
	if err != nil {
		return "", a.asRoot(func() error { return os.Rename(slot, a.official()) }, "mv", "-f", slot, a.official())
	}
	target, err := filepath.EvalSymlinks(slot)
	if err != nil {
		return "", err
	}
	if err := replaceLink(target, a.official()); err != nil {
		return "", err
	}
	return link, a.asRoot(func() error { return os.Remove(slot) }, "rm", "-f", slot)
}

// takeFromHomebrew unlinks the formula and puts the shim at the next free
// place after Homebrew's in the app's list.
func (a appSlot) takeFromHomebrew(i int, formula, prefix string, log func(string)) error {
	slot := a.freeAfter(i)
	if slot == "" {
		return errors.New("the Llama app runs Homebrew's llama, and no later place in its list is free: " + strings.Join(a.candidates[i+1:], ", "))
	}
	opt := filepath.Join(prefix, "opt", formula, "bin", "llama")
	if !isProgram(opt) {
		return errors.New("Homebrew's llama is not at " + opt)
	}
	if err := replaceLink(opt, a.official()); err != nil {
		return err
	}
	brew := filepath.Join(prefix, "bin", "brew")
	if err := a.run(brew, "unlink", formula); err != nil {
		return fmt.Errorf("brew unlink %s: %w", formula, err)
	}
	if err := a.linkShim(slot); err != nil {
		return errors.Join(err, a.run(brew, "link", formula))
	}
	log("unlinked Homebrew's " + formula + "; the Llama app now runs llamaenv's llama at " + slot)
	return writeRecord(a.record(), slotRecord{Slot: slot, Brew: brew, Formula: formula})
}

// freeAfter returns the first place after index i that is free or already
// the shim, or "".
func (a appSlot) freeAfter(i int) string {
	for _, c := range a.candidates[i+1:] {
		if a.isShim(c) || !exists(c) {
			return c
		}
	}
	return ""
}

func (a appSlot) linkShim(slot string) error {
	if a.isShim(slot) {
		return nil
	}
	return a.asRoot(func() error { return os.Symlink(a.shim, slot) }, "ln", "-s", a.shim, slot)
}

// release undoes take. When something else replaced the shim in the
// meantime, such as an update of the app's llama, that file stays.
func (a appSlot) release(log func(string)) error {
	rec, err := readRecord(a.record())
	if err != nil || rec.Slot == "" {
		return err
	}
	if !a.isShim(rec.Slot) {
		log("the Llama app runs " + rec.Slot + " again without llamaenv; leaving it")
		return a.relink(rec, log)
	}
	if err := a.asRoot(func() error { return os.Remove(rec.Slot) }, "rm", "-f", rec.Slot); err != nil {
		return err
	}
	if rec.Brew == "" {
		if err := a.putBack(rec); err != nil {
			return fmt.Errorf("put the official llama back at %s: %w", rec.Slot, err)
		}
		log("put the official llama back at " + rec.Slot)
	}
	return a.relink(rec, log)
}

// putBack restores the symlink or the program that take kept.
func (a appSlot) putBack(rec slotRecord) error {
	if rec.Link != "" {
		return a.asRoot(func() error { return os.Symlink(rec.Link, rec.Slot) }, "ln", "-s", rec.Link, rec.Slot)
	}
	return a.asRoot(func() error { return os.Rename(a.official(), rec.Slot) }, "mv", "-f", a.official(), rec.Slot)
}

// relink links the Homebrew formula again when llamaenv unlinked it and
// nothing linked it since.
func (a appSlot) relink(rec slotRecord, log func(string)) error {
	if rec.Brew == "" || exists(filepath.Join(filepath.Dir(rec.Brew), "llama")) {
		return nil
	}
	if err := a.run(rec.Brew, "link", rec.Formula); err != nil {
		return fmt.Errorf("brew link %s: %w", rec.Formula, err)
	}
	log("linked Homebrew's " + rec.Formula + " again")
	return nil
}

// status says which llama the app runs.
func (a appSlot) status() string {
	if len(a.candidates) == 0 {
		return ""
	}
	i := a.current()
	switch {
	case i < 0:
		return "Llama app: finds no llama"
	case a.isShim(a.candidates[i]):
		return "Llama app: runs llamaenv's llama at " + a.candidates[i]
	}
	return "Llama app: runs " + a.candidates[i] + " without llamaenv; run 'llamaenv install' to switch it"
}

// asRoot runs op, and runs the same change as a command through sudo when op
// is not permitted, as in /usr/local/bin.
func (a appSlot) asRoot(op func() error, cmd ...string) error {
	err := op()
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	return a.run("sudo", cmd...)
}

// homebrew says whether path is a Homebrew formula's llama and names the
// formula and the prefix. The app checks for the Cellar the same way.
func homebrew(path string) (formula, prefix string, ok bool) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(filepath.ToSlash(resolved), "/")
	for i, p := range parts {
		if p == "Cellar" && i+1 < len(parts) {
			return parts[i+1], filepath.Dir(filepath.Dir(path)), true
		}
	}
	return "", "", false
}

func isProgram(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Mode().Perm()&0o111 != 0
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func replaceLink(target, link string) error {
	if err := os.Remove(link); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Symlink(target, link)
}

func writeRecord(path string, rec slotRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func readRecord(path string) (slotRecord, error) {
	var rec slotRecord
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return rec, nil
	}
	if err != nil {
		return rec, err
	}
	return rec, json.Unmarshal(data, &rec)
}
