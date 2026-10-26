package main

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Status is what a session is doing, as far as its hooks have told us.
type Status string

const (
	StatusStarting Status = "starting"
	StatusWorking  Status = "working"
	StatusReady    Status = "ready"     // waiting for the next prompt
	StatusNeedsYou Status = "needs-you" // a permission prompt or a question
	StatusExited   Status = "exited"    // the process ended while the server ran
	StatusStopped  Status = "stopped"   // the server restarted; resumable
)

// scrollbackLimit caps the output kept for replay when a browser reconnects.
const scrollbackLimit = 1 << 20

// Session is one claude process in a pty. The fields above mu are persisted.
type Session struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Cwd             string    `json:"cwd"`
	Args            []string  `json:"args"`
	ClaudeSessionID string    `json:"claudeSessionId"`
	CreatedAt       time.Time `json:"createdAt"`

	mu          sync.Mutex
	status      Status
	statusSince time.Time
	unseen      bool
	cmd         *exec.Cmd
	ptmx        *os.File
	scrollback  []byte
	lastOutput  time.Time
	clients     map[chan []byte]struct{}
}

// SessionView is what the browser sees of a session.
type SessionView struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Cwd             string    `json:"cwd"`
	ClaudeSessionID string    `json:"claudeSessionId"`
	Status          Status    `json:"status"`
	StatusSince     time.Time `json:"statusSince"`
	Unseen          bool      `json:"unseen"`
	PID             int       `json:"pid"`
}

func (s *Session) View() SessionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := SessionView{
		ID:              s.ID,
		Name:            s.Name,
		Cwd:             s.Cwd,
		ClaudeSessionID: s.ClaudeSessionID,
		Status:          s.status,
		StatusSince:     s.statusSince,
		Unseen:          s.unseen,
	}
	if s.cmd != nil && s.cmd.Process != nil {
		v.PID = s.cmd.Process.Pid
	}
	return v
}

// setStatus records a new status and reports whether it changed. Ending up
// ready or needing you marks the session unseen until the browser opens it.
func (s *Session) setStatus(st Status) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == st {
		return false
	}
	s.status = st
	s.statusSince = time.Now()
	if st == StatusReady || st == StatusNeedsYou || st == StatusExited {
		s.unseen = true
	}
	return true
}

func (s *Session) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ptmx != nil
}

// start runs claude with args in a new pty and pumps its output until it exits,
// then calls onExit.
func (s *Session) start(claudeArgs []string, env []string, onExit func()) error {
	cmd := exec.Command("claude", claudeArgs...)
	cmd.Dir = s.Cwd
	cmd.Env = env
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 40})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cmd, s.ptmx = cmd, ptmx
	s.scrollback = nil
	s.mu.Unlock()
	s.setStatus(StatusStarting)

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				s.broadcast(buf[:n])
			}
			if err != nil {
				break
			}
		}
		_ = cmd.Wait()
		s.mu.Lock()
		_ = s.ptmx.Close()
		s.ptmx = nil
		s.mu.Unlock()
		onExit()
	}()
	return nil
}

func (s *Session) broadcast(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastOutput = time.Now()
	s.scrollback = append(s.scrollback, p...)
	if over := len(s.scrollback) - scrollbackLimit; over > 0 {
		s.scrollback = s.scrollback[over:]
	}
	chunk := append([]byte(nil), p...)
	for c := range s.clients {
		select {
		case c <- chunk:
		default: // a client that can't keep up loses the chunk; a reload fixes it
		}
	}
}

// attach returns the scrollback so far and a channel of everything after it.
func (s *Session) attach() ([]byte, chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clients == nil {
		s.clients = map[chan []byte]struct{}{}
	}
	c := make(chan []byte, 256)
	s.clients[c] = struct{}{}
	return append([]byte(nil), s.scrollback...), c
}

func (s *Session) detach(c chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, c)
}

func (s *Session) write(p []byte) {
	s.mu.Lock()
	ptmx := s.ptmx
	s.mu.Unlock()
	if ptmx != nil {
		_, _ = ptmx.Write(p)
	}
}

func (s *Session) resize(cols, rows uint16) {
	s.mu.Lock()
	ptmx := s.ptmx
	s.mu.Unlock()
	if ptmx != nil && cols > 0 && rows > 0 {
		_ = pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows})
	}
}

// kill hangs up the session the way closing a terminal window does, and
// kills it if it is still there after a few seconds.
func (s *Session) kill() {
	s.mu.Lock()
	cmd := s.cmd
	running := s.ptmx != nil
	s.mu.Unlock()
	if !running || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGHUP)
	go func() {
		time.Sleep(3 * time.Second)
		if s.running() {
			_ = cmd.Process.Kill()
		}
	}()
}
