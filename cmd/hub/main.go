// claude-mux runs Claude Code sessions behind one web page: tabs on the left,
// the terminal on the right, and a dot that says which session waits for you.
// The sessions live in backends (claude-mux-backend), one per environment,
// which it runs as children: on this machine, over SSH, in Docker containers
// and in WSL.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/askolesov/claude-mux/internal/hub"
)

// version is the release tag, set by the release build; a release hub
// installs the backends of the same release.
var version = "dev"

func main() {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	stateDir := filepath.Join(home, ".claude-mux")
	addr := flag.String("addr", "127.0.0.1:7777", "listen address; keep it on loopback, the page can start processes")
	backendDir := flag.String("backend-dir", "", "take the backend builds, claude-mux-backend-<os>-<arch>, from this directory instead of the release")
	defaultCwd := flag.String("cwd", cwd, "default directory for new local tabs")
	ssh := flag.String("ssh", "ssh", "the ssh command, with any options of its own")
	docker := flag.String("docker", "docker", "the docker command")
	wsl := flag.String("wsl", "wsl.exe", "the WSL command")
	flag.StringVar(&stateDir, "state", stateDir, "where environments.json and the local backend's state are kept")
	open := flag.Bool("open", runtime.GOOS == "windows", "open the page in the browser")
	printVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *printVersion {
		fmt.Println(version)
		return
	}

	backends := hub.Backends{Dir: *backendDir, Version: version, Cache: filepath.Join(stateDir, "cache")}
	h, err := hub.New(stateDir, backends, hub.Connectors{SSH: strings.Fields(*ssh), Docker: strings.Fields(*docker), WSL: strings.Fields(*wsl)}, *defaultCwd)
	if err == nil {
		err = h.Run(*addr, *open)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "claude-mux:", err)
		os.Exit(1)
	}
}
