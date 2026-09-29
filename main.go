// Command llamaenv runs each model with the llama.cpp runtime it needs.
//
// The same program is installed twice: as "llamaenv" for setup and
// diagnosis, and as "llama" first on PATH, where it acts as the shim.
package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/osolmaz/llamaenv/internal/cli"
	"github.com/osolmaz/llamaenv/internal/proc"
	"github.com/osolmaz/llamaenv/internal/shim"
)

func main() {
	proc.Watcher, _ = os.Executable()
	os.Exit(dispatch(os.Args[0], os.Args[1:]))
}

// dispatch runs the shim when the program is called "llama", and the
// llamaenv command otherwise. The process watcher comes first under both
// names, because llamaenv starts it by its own program path.
func dispatch(program string, args []string) int {
	if len(args) > 0 && args[0] == proc.WatchCommand {
		return proc.Watch(args[1:])
	}
	if strings.TrimSuffix(strings.ToLower(filepath.Base(program)), ".exe") == "llama" {
		return shim.Main(args)
	}
	return cli.Main(args, os.Stdout, os.Stderr)
}
