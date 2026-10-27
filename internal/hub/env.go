package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/askolesov/claude-mux/internal/sysproc"
	"github.com/hashicorp/yamux"
)

var errNotConnected = errors.New("the environment's backend isn't running")

// EnvConfig is an environment as hub.json keeps it.
type EnvConfig struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`             // local, ssh, docker, wsl
	Target string `json:"target,omitempty"` // the host as ssh takes it, the container, the WSL distro
}

// Connection states, as the page shows them.
const (
	StateConnecting = "connecting"
	StateConnected  = "connected"
	StateFailed     = "failed" // Error says why; the hub tries again
)

// EnvView is what the page sees of an environment.
type EnvView struct {
	EnvConfig
	State    string `json:"state"`
	Platform string `json:"platform,omitempty"` // "linux-arm64", once connected
	Latency  int    `json:"latency,omitempty"`  // ms, the last ping
	Step     string `json:"step,omitempty"`     // while connecting: what it is doing
	Error    string `json:"error,omitempty"`
}

// Env is one environment: a backend the hub runs as its child and talks to
// over the child's stdin and stdout. A backend that stops is started again.
type Env struct {
	EnvConfig
	prepare  func(step func(string)) ([]string, string, error) // installs the backend if needed; the command that runs it and the platform
	onChange func()
	// onConnect runs once the backend answers, each time.
	onConnect func(*Env)

	mu        sync.Mutex
	session   *yamux.Session
	cmd       *exec.Cmd
	state     string
	step      string
	platform  string
	latency   time.Duration
	reconnect bool // the next exit is a reconnect, not a failure
	err       string

	quit chan struct{} // closed by Close
	done chan struct{} // closed when Supervise returns

	proxy *httputil.ReverseProxy
}

func NewEnv(cfg EnvConfig, prepare func(step func(string)) ([]string, string, error), onChange func()) *Env {
	e := &Env{EnvConfig: cfg, prepare: prepare, onChange: onChange, state: StateConnecting,
		quit: make(chan struct{}), done: make(chan struct{})}
	e.proxy = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme, r.Out.URL.Host = "http", "backend"
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

func (e *Env) View() EnvView {
	e.mu.Lock()
	defer e.mu.Unlock()
	return EnvView{EnvConfig: e.EnvConfig, State: e.state, Platform: e.platform, Latency: int(e.latency.Milliseconds()),
		Step: e.step, Error: e.err}
}

func (e *Env) setState(state, err string) {
	e.mu.Lock()
	changed := e.state != state || e.err != err || e.step != ""
	e.state, e.err, e.step = state, err, ""
	e.mu.Unlock()
	if changed {
		e.onChange()
	}
}

// call sends one request to the backend, with body as JSON.
func (e *Env) call(method, path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, "http://backend"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := e.proxy.Transport.RoundTrip(req.WithContext(ctx))
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s", method, path, res.Status)
	}
	return nil
}

// Reconnect starts the backend again: the running one hangs up its tabs,
// and the new one resumes them.
func (e *Env) Reconnect() {
	e.mu.Lock()
	session := e.session
	if session != nil {
		e.reconnect = true
	}
	e.mu.Unlock()
	if session != nil {
		_ = session.Close()
	}
}

// setLatency keeps the last ping; the page hears of it when it moves by
// more than a few ms.
func (e *Env) setLatency(rtt time.Duration) {
	e.mu.Lock()
	diff := rtt - e.latency
	e.latency = rtt
	e.mu.Unlock()
	if diff > 5*time.Millisecond || diff < -5*time.Millisecond {
		e.onChange()
	}
}

// setStep says what connecting is doing now.
func (e *Env) setStep(step string) {
	e.mu.Lock()
	changed := e.step != step
	e.step = step
	e.mu.Unlock()
	if changed {
		e.onChange()
	}
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

// Supervise runs the backend until Close, starting it again after it stops,
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
		e.mu.Lock()
		again := e.reconnect
		e.reconnect = false
		e.mu.Unlock()
		if again {
			continue
		}
		e.setState(StateFailed, err.Error())
		fmt.Fprintf(os.Stderr, "env %s: %v; trying again in %s\n", e.Name, err, pause)
		select {
		case <-e.quit:
			return
		case <-time.After(pause):
		}
		pause = min(2*pause, 30*time.Second)
	}
}

// run prepares and starts the backend and returns when it exits, with the
// reason: its last line on stderr, if it left one.
func (e *Env) run() error {
	e.setState(StateConnecting, "")
	argv, plat, err := e.prepare(e.setStep)
	e.setStep("starting the backend")
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.platform = plat
	e.mu.Unlock()
	cmd := sysproc.Hide(exec.Command(argv[0], argv[1:]...))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &lastLine{prefix: "env " + e.Name + ": "}
	cmd.Stderr = stderr
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
	go func() {
		// Connected once the backend answers; over SSH that can take a while.
		// Then a ping now and then, for the page to show the latency.
		rtt, err := session.Ping()
		if err != nil {
			return
		}
		e.setLatency(rtt)
		e.setState(StateConnected, "")
		if e.onConnect != nil {
			e.onConnect(e)
		}
		for {
			select {
			case <-session.CloseChan():
				return
			case <-time.After(15 * time.Second):
			}
			if rtt, err := session.Ping(); err == nil {
				e.setLatency(rtt)
			}
		}
	}()

	err = cmd.Wait()
	e.mu.Lock()
	e.session, e.cmd = nil, nil
	e.mu.Unlock()
	_ = session.Close()
	if line := stderr.Last(); line != "" {
		return errors.New(line)
	}
	if err == nil {
		err = errors.New("the backend exited")
	}
	return err
}

// Close ends the backend the way a quitting hub does: it closes the pipe, so
// the backend hangs up its sessions, keeping its tabs, and exits. It waits
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

// lastLine passes a child's stderr on to the hub's, prefixed, and keeps its
// last line: why the child stopped.
type lastLine struct {
	prefix string

	mu   sync.Mutex
	buf  []byte
	last string
}

func (l *lastLine) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := strings.TrimSpace(string(l.buf[:i]))
		l.buf = l.buf[i+1:]
		fmt.Fprintln(os.Stderr, l.prefix+line)
		if line != "" {
			l.last = line
		}
	}
}

func (l *lastLine) Last() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last
}
