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
	global := flag.String("global", "", "the startup message every tab gets before its own: base64url of {\"enabled\", \"message\"}")
	resume := flag.String("resume", "", "the resume default from before v0.7, to upgrade backend.json with; the same encoding")
	flag.Parse()

	parse := func(name, value string) backend.StartupMessage {
		if value == "" {
			return backend.StartupMessage{}
		}
		m, err := backend.ParseStartupFlag(value)
		if err != nil {
			fmt.Fprintf(os.Stderr, "claude-mux-backend: -%s: %v\n", name, err)
			os.Exit(2)
		}
		return m
	}

	if err := backend.Run(*stateDir, *defaultCwd, parse("global", *global), parse("resume", *resume)); err != nil {
		fmt.Fprintln(os.Stderr, "claude-mux-backend:", err)
		os.Exit(1)
	}
}
