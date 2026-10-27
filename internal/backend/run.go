package backend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"
)

// Run serves the backend to the hub over stdin and stdout until the hub
// closes them: the hub quit or the connection to it broke. It then hangs up
// its sessions, noting which ran, so the next start resumes them.
func Run(stateDir, defaultCwd string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	// Hooks can't reach the hub's pipe, so they get a port of their own.
	hookLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	token := make([]byte, 16)
	_, _ = rand.Read(token)
	hookURL := fmt.Sprintf("http://%s/hook/%s", hookLn.Addr(), hex.EncodeToString(token))

	m, err := NewManager(stateDir, hookURL, exe)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	go m.PollNames(ctx, 5*time.Second)
	go m.WatchStartup(ctx)

	hooks := &http.Server{Handler: NewHookHandler(m, hex.EncodeToString(token)), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = hooks.Serve(hookLn) }()

	// The hub opens a stream per request and per terminal; yamux's session
	// is the listener the API is served on.
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = os.Stderr
	session, err := yamux.Server(stdio{}, cfg)
	if err != nil {
		return err
	}
	api := &http.Server{Handler: NewHandler(m, defaultCwd), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = api.Serve(session) }()

	select {
	case <-session.CloseChan():
	case <-ctx.Done():
	}
	m.Shutdown()
	_ = session.Close()
	_ = hooks.Close()
	return nil
}

// stdio is the pipe to the hub. Nothing else may write to stdout: logs go to
// stderr, which the hub keeps.
type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return errors.Join(os.Stdin.Close(), os.Stdout.Close()) }
