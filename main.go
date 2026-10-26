// claude-mux runs Claude Code sessions behind one web page: tabs on the left,
// the terminal on the right, and a dot that says which session waits for you.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	addr := flag.String("addr", "127.0.0.1:7777", "listen address; keep it on loopback, the page can start processes")
	stateDir := flag.String("state", filepath.Join(home, ".claude-mux"), "where sessions and the hooks file are kept")
	defaultCwd := flag.String("cwd", cwd, "default directory for new sessions")
	resumeRunning := flag.Bool("resume-running", false, "bring back the tabs that were running when the server last stopped (restart.sh sets it)")
	flag.Parse()

	if err := run(*addr, *stateDir, *defaultCwd, *resumeRunning); err != nil {
		fmt.Fprintln(os.Stderr, "claude-mux:", err)
		os.Exit(1)
	}
}

func run(addr, stateDir, defaultCwd string, resumeRunning bool) error {
	if _, err := exec.LookPath("claude"); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("refusing to listen on %s: the page runs commands, keep it on loopback", addr)
	}

	m, err := NewManager(stateDir, "http://"+addr+"/hook", resumeRunning)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ext := &Externals{m: m}
	go ext.Poll(ctx, 5*time.Second)
	go m.WatchStartup(ctx)

	srv := &http.Server{Addr: addr, Handler: NewHandler(m, ext, defaultCwd), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	fmt.Printf("claude-mux on http://%s\n", addr)

	select {
	case err = <-errc:
	case <-ctx.Done():
	}
	m.Shutdown()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	return err
}
