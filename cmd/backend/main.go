// claude-mux-backend runs Claude Code sessions in the environment it runs in
// and serves them to the claude-mux hub over its stdin and stdout. The hub
// starts it; `claude-mux-backend hook` is the command claude's hooks run.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/askolesov/claude-mux/internal/backend"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		backend.Hook()
		return
	}
	home, _ := os.UserHomeDir()
	stateDir := flag.String("state", filepath.Join(home, ".claude-mux"), "where sessions, saved conversations and the hooks file are kept")
	defaultCwd := flag.String("cwd", home, "default directory for new sessions")
	flag.Parse()

	if err := backend.Run(*stateDir, *defaultCwd); err != nil {
		fmt.Fprintln(os.Stderr, "claude-mux-backend:", err)
		os.Exit(1)
	}
}
