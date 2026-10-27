// claude-mux runs Claude Code sessions behind one web page: tabs on the left,
// the terminal on the right, and a dot that says which session waits for you.
// The sessions live in backends (claude-mux-backend), one per environment,
// which it runs as children: on this machine, over SSH, in Docker containers
// and in WSL. The page shows in a window of its own, or with -headless in the
// browser.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/askolesov/claude-mux/internal/hub"
)

// version is the release tag, set by the release build; a release hub
// installs the backends of the same release.
var version = "dev"

func main() {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	// Started from Finder or Explorer rather than a terminal: no terminal
	// environment, and the directory is / or the program's own.
	gui := os.Getenv("TERM") == ""
	if gui {
		cwd = home
	}
	stateDir := filepath.Join(home, ".claude-mux")
	addr := flag.String("addr", "127.0.0.1:7777", "listen address; keep it on loopback, the page can start processes")
	backendDir := flag.String("backend-dir", "", "take the backend builds, claude-mux-backend-<os>-<arch>, from this directory instead of the release")
	defaultCwd := flag.String("cwd", cwd, "default directory for new local tabs")
	ssh := flag.String("ssh", "ssh", "the ssh command, with any options of its own")
	docker := flag.String("docker", "docker", "the docker command")
	wsl := flag.String("wsl", "wsl.exe", "the WSL command")
	flag.StringVar(&stateDir, "state", stateDir, "where environments.json and the local backend's state are kept")
	headless := flag.Bool("headless", false, "no window: serve the page until interrupted")
	open := flag.Bool("open", false, "with -headless, open the page in the browser")
	printVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *printVersion {
		fmt.Println(version)
		return
	}
	if gui {
		logToFile(filepath.Join(stateDir, "hub.log"))
		loginShellEnv()
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	url := "http://" + *addr
	backends := hub.Backends{Dir: *backendDir, Version: version, Cache: filepath.Join(stateDir, "cache")}
	connectors := hub.Connectors{SSH: strings.Fields(*ssh), Docker: strings.Fields(*docker), WSL: strings.Fields(*wsl)}
	h, err := hub.New(stateDir, backends, connectors, *defaultCwd)
	if err != nil {
		fail(err)
	}
	stop, err := h.Start(*addr)
	if err != nil {
		// Already running: a second launch shows the running hub's page.
		if !*headless && answers(url) {
			window(ctx, url)
			return
		}
		fail(err)
	}
	switch {
	case !*headless:
		window(ctx, url) // until the window closes
	case *open:
		openBrowser(url)
		fallthrough
	default:
		<-ctx.Done()
	}
	stop()
}

func fail(err error) {
	log.Print("claude-mux: ", err)
	os.Exit(1)
}

// answers says whether a hub is serving url.
func answers(url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Get(url + "/hub/info")
	if err != nil {
		return false
	}
	_ = res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// logToFile sends the hub's output, and its backends', to a file: a program
// started from Finder or Explorer has nowhere else to put it.
func logToFile(path string) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	os.Stderr = f
	log.SetOutput(f)
}

// loginShellEnv takes the environment a login shell would have. Started from
// Finder, the hub gets launchd's bare PATH, and claude in a local tab would
// miss everything the user installed.
func loginShellEnv() {
	shell := os.Getenv("SHELL")
	if runtime.GOOS == "windows" || shell == "" {
		return
	}
	const marker = "__claude_mux_env__"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, shell, "-ilc", "printf "+marker+"; env -0").Output()
	if err != nil {
		log.Print("claude-mux: login shell environment: ", err)
		return
	}
	_, env, ok := strings.Cut(string(out), marker)
	if !ok {
		return
	}
	for kv := range strings.SplitSeq(env, "\x00") {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k != "" && k != "PWD" && k != "OLDPWD" && k != "SHLVL" && k != "_" {
			_ = os.Setenv(k, v)
		}
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}
