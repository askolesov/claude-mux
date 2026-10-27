// claude-mux-backend runs Claude Code sessions in the environment it runs in
// and serves them to the claude-mux hub over its stdin and stdout. The hub
// starts it; `claude-mux-backend hook` is the command claude's hooks run.
package main

import (
	"encoding/base64"
	"encoding/json"
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
	resume := flag.String("resume", "", "the message tabs get when they start again, as the hub sets it: base64url of {\"enabled\", \"message\"}")
	flag.Parse()

	var d backend.ResumeDefault
	if *resume != "" {
		data, err := base64.RawURLEncoding.DecodeString(*resume)
		if err == nil {
			err = json.Unmarshal(data, &d)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "claude-mux-backend: -resume:", err)
			os.Exit(2)
		}
	}

	if err := backend.Run(*stateDir, *defaultCwd, d); err != nil {
		fmt.Fprintln(os.Stderr, "claude-mux-backend:", err)
		os.Exit(1)
	}
}
