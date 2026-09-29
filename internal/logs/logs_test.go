package logs

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestFileStaysWithinItsLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "a.log")
	f, err := Open(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 29) + "\n" // 30 bytes
	for range 10 {
		if _, err := f.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// 300 bytes in 100-byte files of whole lines: the newest 30 and 90 stay.
	if got, old := read(t, path), read(t, path+".1"); got != strings.Repeat(line, 1) || old != strings.Repeat(line, 3) {
		t.Errorf("current %d bytes, old %d bytes", len(got), len(old))
	}
	if _, err := f.Write([]byte(line)); err == nil {
		t.Error("a write after Close must fail")
	}
}

func TestOpenKeepsThePreviousRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	for _, run := range []string{"first\n", "second\n", "third\n"} {
		f, err := Open(path, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(run))
		_ = f.Close()
	}
	if read(t, path) != "third\n" || read(t, path+".1") != "second\n" {
		t.Errorf("got %q and %q", read(t, path), read(t, path+".1"))
	}
}

func TestNilFileDiscards(t *testing.T) {
	var f *File
	if n, err := f.Write([]byte("abc")); n != 3 || err != nil || f.Close() != nil {
		t.Errorf("nil file: %d %v", n, err)
	}
}

func TestLinesSplitsAndCuts(t *testing.T) {
	var got []string
	w := NewLines(func(l string) { got = append(got, l) })
	long := strings.Repeat("y", MaxLine+10)
	for _, part := range []string{"one\r\ntw", "o\n", long + "\nlast"} {
		if n, err := w.Write([]byte(part)); n != len(part) || err != nil {
			t.Fatalf("write: %d %v", n, err)
		}
	}
	_ = w.Close()
	want := []string{"one", "two", long[:MaxLine], "last"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %d lines: %.40q", len(got), got)
	}
}

func TestQueueDropsInsteadOfBlocking(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var got []string
	q := NewQueue(func(l string) {
		<-release
		mu.Lock()
		got = append(got, l)
		mu.Unlock()
	}, 2)
	done := make(chan struct{})
	go func() {
		for range 10 {
			q.Add("line")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Add blocked on a sink that does not read")
	}
	// One line is in the sink, two wait in the queue, the rest are dropped.
	if q.Dropped() < 7 {
		t.Errorf("dropped %d, want at least 7", q.Dropped())
	}
	if q.Close(50 * time.Millisecond) {
		t.Error("Close must give up on a stuck sink")
	}
	q.Add("after close")
	close(release)
	if !q.Close(5 * time.Second) {
		t.Error("the queue did not drain")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 || len(got) > 3 {
		t.Errorf("sink got %d lines", len(got))
	}
}

func TestFileRecoversFromAFailedOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	f, err := Open(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	// A folder in the way of the file, which cannot move onto the old file,
	// makes the next open fail.
	_ = f.f.Close()
	f.f = nil
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".1", []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("lost\n")); err == nil {
		t.Error("a write without a file must fail")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("kept\n")); err != nil || read(t, path) != "kept\n" {
		t.Errorf("write after recovery: %v", err)
	}
}
