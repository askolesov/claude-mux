// claude-mux runs Claude Code sessions behind one web page: tabs on the left,
// the terminal on the right, and a dot that says which session waits for you.
// The sessions live in backends (claude-mux-backend), which it runs as
// children.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/askolesov/claude-mux/internal/hub"
)

func main() {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	addr := flag.String("addr", "127.0.0.1:7777", "listen address; keep it on loopback, the page can start processes")
	backend := flag.String("backend", filepath.Join(home, ".claude-mux", "bin", "dev", "claude-mux-backend"), "the local backend's binary")
	defaultCwd := flag.String("cwd", cwd, "default directory for new local sessions")
	flag.Parse()

	if err := hub.Run(*addr, []string{*backend, "-cwd", *defaultCwd}); err != nil {
		fmt.Fprintln(os.Stderr, "claude-mux:", err)
		os.Exit(1)
	}
}
