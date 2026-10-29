package backend

import (
	"sync"
	"time"
)

// Status is what a session is doing, as far as its hooks have told us.
type Status string

const (
	StatusStarting Status = "starting"
	StatusIdle     Status = "idle" // a terminal at its prompt, or a codex before its first hook
	StatusWorking  Status = "working"
	StatusReady    Status = "ready"     // waiting for the next prompt
	StatusNeedsYou Status = "needs-you" // a permission prompt or a question
	StatusExited   Status = "exited"    // the process ended while the backend ran
	StatusStopped  Status = "stopped"   // loaded from backend.json and not started yet (no claude here); resumable
)

// scrollbackLimit caps the output kept for replay when a terminal connects.
const scrollbackLimit = 1 << 20

// Session is a tab: one process in a pty, claude, codex or a shell (Kind). The fields above mu are
// persisted in the state file.
type Session struct {
	ID             string     `json:"id"`
	Kind           Kind       `json:"kind"`
	Name           string     `json:"name"`
	Parent         string     `json:"parent,omitempty"` // the group it is in, "" for none (see tree.go)
	Order          string     `json:"order"`
	Cwd            string     `json:"cwd"`
	Args           []string   `json:"args,omitempty"`
	AgentSessionID string     `json:"agentSessionId,omitempty"` // the agent's conversation the tab continues
	Resume         ResumeMode `json:"resume,omitempty"`         // the first prompt when its claude starts again
	ResumeMessage  string     `json:"resumeMessage,omitempty"`

	mu          sync.Mutex
	status      Status
	statusSince time.Time
	unseen      bool
	branch      string   // the git branch of Cwd, if it is in a repository
	restarting  bool     // the next exit starts it again
	started     bool     // a SessionStart came since the process started (ApplyHook)
	proc        *ptyProc // nil while claude isn't running
	scrollback  []byte
	lastOutput  time.Time
	cwdAt       time.Time // when a terminal's folder was last read (followCwd)
	cols, rows  uint16    // the page's last terminal size; a resumed pty starts with it
	clients     map[chan []byte]struct{}
}

// SessionView is what the page sees of a session.
type SessionView struct {
	ID             string     `json:"id"`
	Kind           Kind       `json:"kind"`
	Name           string     `json:"name"`
	Parent         string     `json:"parent"`
	Order          string     `json:"order"`
	Cwd            string     `json:"cwd"`
	Args           []string   `json:"args"`
	AgentSessionID string     `json:"agentSessionId"`
	Resume         ResumeMode `json:"resume"`
	ResumeMessage  string     `json:"resumeMessage"`
	Status         Status     `json:"status"`
	StatusSince    time.Time  `json:"statusSince"`
	Unseen         bool       `json:"unseen"`
	Branch         string     `json:"branch"`
}

// View is the tab as the page sees it.
func (s *Session) View() SessionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SessionView{
		ID:             s.ID,
		Kind:           s.Kind,
		Name:           s.Name,
		Parent:         s.Parent,
		Order:          s.Order,
		Cwd:            s.Cwd,
		Args:           s.Args,
		AgentSessionID: s.AgentSessionID,
		Resume:         s.Resume,
		ResumeMessage:  s.ResumeMessage,
		Status:         s.status,
		StatusSince:    s.statusSince,
		Unseen:         s.unseen,
		Branch:         s.branch,
	}
}

// setStatus records a new status and reports whether it changed. Ending up
// ready or needing you marks the session unseen until the page shows it.
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
	return s.proc != nil
}

// start runs claude (its path) with args in a new pty and pumps its output
// until it exits, then calls onExit.
func (s *Session) start(claude string, claudeArgs []string, env []string, onExit func()) error {
	s.mu.Lock()
	cwd, cols, rows := s.Cwd, s.cols, s.rows
	s.mu.Unlock()
	if cols == 0 {
		cols, rows = 120, 40
	}
	proc, err := startPty(claude, claudeArgs, env, cwd, cols, rows)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.proc = proc
	s.scrollback = nil
	s.started = false
	s.mu.Unlock()
	s.setStatus(StatusStarting)

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := proc.Read(buf)
			if n > 0 {
				s.broadcast(buf[:n])
			}
			if err != nil {
				break
			}
		}
		_ = proc.Wait()
		s.mu.Lock()
		s.proc = nil
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
	proc := s.proc
	s.mu.Unlock()
	if proc != nil {
		_, _ = proc.Write(p)
	}
}

func (s *Session) resize(cols, rows uint16) {
	if cols == 0 || rows == 0 {
		return
	}
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	proc := s.proc
	s.mu.Unlock()
	if proc != nil {
		_ = proc.Resize(cols, rows)
	}
}

// kill hangs up the session the way closing a terminal window does, and
// kills it if it is still there after a few seconds.
func (s *Session) kill() {
	s.mu.Lock()
	proc := s.proc
	s.mu.Unlock()
	if proc == nil {
		return
	}
	proc.Hangup()
	go func() {
		time.Sleep(3 * time.Second)
		if s.running() {
			proc.Kill()
		}
	}()
}
