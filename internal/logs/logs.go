// Package logs keeps llamaenv's diagnostics in files of bounded size. Nothing
// in it blocks on a slow reader: a llama.cpp logger blocks when its output
// stops draining, and it must never be llamaenv that stops draining it.
package logs

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// MaxLine is the longest line kept. Longer lines are cut, so that one long
// line cannot fill a file.
const MaxLine = 4 << 10

// File is a log file of bounded size. When a write would take it past its
// limit, the file moves to <path>.1, replacing the older one, and a new file
// starts. A nil *File discards what it gets.
type File struct {
	mu     sync.Mutex
	path   string
	max    int64
	f      *os.File // nil after a failed open: the next write tries again
	size   int64
	closed bool
}

// Open starts a new log file at path. The previous run's file moves to
// <path>.1 first, so that it survives a restart.
func Open(path string, maxBytes int64) (*File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	l := &File{path: path, max: maxBytes}
	if err := l.rotate(); err != nil {
		return nil, err
	}
	return l, nil
}

// Write writes p as one piece: a rotation never splits it.
func (l *File) Write(p []byte) (int, error) {
	if l == nil {
		return len(p), nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, fs.ErrClosed
	}
	if l.f == nil || l.size > 0 && l.size+int64(len(p)) > l.max {
		if err := l.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

// rotate moves the current file to <path>.1 and opens a new one. Callers hold
// mu, or own l alone.
func (l *File) rotate() error {
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
	// Windows cannot move a file that another program has open. The file then
	// starts over in place, which loses its lines but keeps the limit.
	_ = os.Rename(l.path, l.path+".1")
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	l.f, l.size = f, 0
	return nil
}

// Close closes the file. Later writes fail.
func (l *File) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Lines is a writer that splits what it gets into lines, cut at MaxLine, and
// hands each line, without its newline, to a function. It is meant as a
// process's output, so the function must not block.
type Lines struct {
	fn       func(line string)
	buf      []byte
	skipping bool // the current line was cut; drop the rest of it
}

// NewLines returns a Lines that calls fn for each line.
func NewLines(fn func(line string)) *Lines { return &Lines{fn: fn} }

func (w *Lines) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		part := p
		if i >= 0 {
			part = p[:i]
		}
		if !w.skipping {
			room := MaxLine - len(w.buf)
			if len(part) > room {
				w.buf = append(w.buf, part[:room]...)
				w.emit()
				w.skipping = true
			} else {
				w.buf = append(w.buf, part...)
			}
		}
		if i < 0 {
			break
		}
		if !w.skipping {
			w.emit()
		}
		w.skipping = false
		p = p[i+1:]
	}
	return n, nil
}

func (w *Lines) emit() {
	w.fn(string(bytes.TrimSuffix(w.buf, []byte("\r"))))
	w.buf = w.buf[:0]
}

// Close hands on the last line when it had no newline.
func (w *Lines) Close() error {
	if len(w.buf) > 0 && !w.skipping {
		w.emit()
	}
	return nil
}

// Queue passes lines to a sink from its own goroutine. When the sink does not
// keep up and the queue is full, new lines are dropped and counted.
type Queue struct {
	mu      sync.RWMutex
	closed  bool
	lines   chan string
	done    chan struct{}
	dropped atomic.Int64
}

// NewQueue starts a queue of up to depth lines in front of sink.
func NewQueue(sink func(line string), depth int) *Queue {
	q := &Queue{lines: make(chan string, depth), done: make(chan struct{})}
	go func() {
		defer close(q.done)
		for line := range q.lines {
			sink(line)
		}
	}()
	return q
}

// Add queues a line. It drops the line when the queue is full or closed.
func (q *Queue) Add(line string) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		q.dropped.Add(1)
		return
	}
	select {
	case q.lines <- line:
	default:
		q.dropped.Add(1)
	}
}

// Dropped returns the number of lines dropped so far.
func (q *Queue) Dropped() int64 { return q.dropped.Load() }

// Close stops taking lines and waits up to timeout for the queued lines to be
// handed on. It returns false when the sink did not keep up.
func (q *Queue) Close(timeout time.Duration) bool {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.lines)
	}
	q.mu.Unlock()
	select {
	case <-q.done:
		return true
	case <-time.After(timeout):
		return false
	}
}
