package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

var errNotConnected = errors.New("the environment's backend isn't running")

// Env is one environment: a backend the hub runs as its child and talks to
// over the child's stdin and stdout. A backend that dies is started again.
type Env struct {
	Name string
	argv []string // the command that starts the backend

	mu      sync.Mutex
	session *yamux.Session
	cmd     *exec.Cmd

	quit chan struct{} // closed by Close
	done chan struct{} // closed when Supervise returns

	proxy *httputil.ReverseProxy
}

func NewEnv(name string, argv []string) *Env {
	e := &Env{Name: name, argv: argv, quit: make(chan struct{}), done: make(chan struct{})}
	e.proxy = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme, r.Out.URL.Host = "http", name
			r.Out.Host = r.In.Host
		},
		// Every request and every terminal is a stream of the pipe.
		Transport: &http.Transport{
			DialContext: func(context.Context, string, string) (net.Conn, error) { return e.open() },
		},
		FlushInterval: -1, // server-sent events go out as they come
	}
	return e
}

// ServeHTTP forwards a request to the backend; the caller has cut the
// environment's prefix from the path.
func (e *Env) ServeHTTP(w http.ResponseWriter, r *http.Request) { e.proxy.ServeHTTP(w, r) }

func (e *Env) open() (net.Conn, error) {
	e.mu.Lock()
	s := e.session
	e.mu.Unlock()
	if s == nil {
		return nil, errNotConnected
	}
	return s.Open()
}

// Supervise runs the backend until Close, starting it again after it dies,
// with a growing pause while it keeps failing.
func (e *Env) Supervise() {
	defer close(e.done)
	pause := time.Second
	for {
		started := time.Now()
		err := e.run()
		if time.Since(started) > time.Minute {
			pause = time.Second
		}
		select {
		case <-e.quit:
			return
		default:
		}
		fmt.Fprintf(os.Stderr, "env %s: backend stopped (%v); starting it again in %s\n", e.Name, err, pause)
		select {
		case <-e.quit:
			return
		case <-time.After(pause):
		}
		pause = min(2*pause, 30*time.Second)
	}
}

// run starts the backend and returns when it exits.
func (e *Env) run() error {
	cmd := exec.Command(e.argv[0], e.argv[1:]...)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	session, err := yamux.Client(pipe{stdout, stdin}, yamuxConfig())
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	e.mu.Lock()
	e.session, e.cmd = session, cmd
	e.mu.Unlock()

	err = cmd.Wait()
	e.mu.Lock()
	e.session, e.cmd = nil, nil
	e.mu.Unlock()
	_ = session.Close()
	return err
}

// Close ends the backend the way a quitting hub does: it closes the pipe, so
// the backend hangs up its sessions, noting which ran, and exits. It waits
// for that, and kills a backend that takes longer than a minute.
func (e *Env) Close() {
	close(e.quit)
	e.mu.Lock()
	session, cmd := e.session, e.cmd
	e.mu.Unlock()
	if session != nil {
		_ = session.Close()
	}
	select {
	case <-e.done:
	case <-time.After(time.Minute):
		if cmd != nil {
			_ = cmd.Process.Kill()
		}
		<-e.done
	}
}

func yamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = os.Stderr
	return cfg
}

// pipe is the child's stdout to read and its stdin to write; closing it
// closes stdin, which the backend reads as the hub going away.
type pipe struct {
	io.Reader
	w io.WriteCloser
}

func (p pipe) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p pipe) Close() error                { return p.w.Close() }
