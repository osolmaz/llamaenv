package cli

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osolmaz/llamaenv/internal/config"
)

// tailLines is how much of each log "llamaenv logs" shows.
const tailLines = 15

// showLogs prints each switcher's log folder, its files, and the end of the
// current event log and runtime logs.
func showLogs(d config.Dirs, _ []string, out io.Writer) error {
	dirs, _ := filepath.Glob(filepath.Join(d.Logs(), "*"))
	sort.Strings(dirs)
	shown := 0
	for _, dir := range dirs {
		files, err := os.ReadDir(dir)
		if err != nil || len(files) == 0 {
			continue
		}
		if shown > 0 {
			say(out, "\n")
		}
		shown++
		showLogDir(dir, files, out)
	}
	if shown == 0 {
		say(out, "no logs in %s\n", d.Logs())
	}
	return nil
}

func showLogDir(dir string, files []os.DirEntry, out io.Writer) {
	say(out, "%s\n", dir)
	for _, f := range files {
		if info, err := f.Info(); err == nil {
			say(out, "  %-28s %8d bytes\n", f.Name(), info.Size())
		}
	}
	for _, f := range files {
		name := f.Name()
		if !strings.HasSuffix(name, ".log") && !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		say(out, "\n== %s ==\n", name)
		for _, line := range tail(filepath.Join(dir, name), tailLines) {
			say(out, "%s\n", line)
		}
	}
}

// tail returns the last n lines of a file.
func tail(path string, n int) []string {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines
}
