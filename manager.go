package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	errNotFound = errors.New("session not found")
	errRunning  = errors.New("session is still running")
	errNoResume = errors.New("session has no claude session id to resume")
)

// Manager owns the sessions, keeps them on disk, and tells subscribers when
// anything changed.
type Manager struct {
	stateFile    string
	hooksFile    string
	hookEndpoint string

	mu       sync.Mutex
	sessions []*Session
	subs     map[chan struct{}]struct{}
}

func NewManager(stateDir, hookEndpoint string) (*Manager, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	m := &Manager{
		stateFile:    filepath.Join(stateDir, "sessions.json"),
		hooksFile:    filepath.Join(stateDir, "hooks.json"),
		hookEndpoint: hookEndpoint,
		subs:         map[chan struct{}]struct{}{},
	}
	if err := writeHookSettings(m.hooksFile, hookEndpoint); err != nil {
		return nil, err
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

// load restores the sessions of a previous run as stopped, ready to resume.
func (m *Manager) load() error {
	data, err := os.ReadFile(m.stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &m.sessions); err != nil {
		return fmt.Errorf("%s: %w", m.stateFile, err)
	}
	for _, s := range m.sessions {
		s.status, s.statusSince = StatusStopped, time.Now()
	}
	return nil
}

func (m *Manager) save() {
	m.mu.Lock()
	data, err := json.MarshalIndent(m.sessions, "", "  ")
	m.mu.Unlock()
	if err == nil {
		err = os.WriteFile(m.stateFile, data, 0o600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "save state:", err)
	}
}

func (m *Manager) Subscribe() chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := make(chan struct{}, 1)
	m.subs[c] = struct{}{}
	return c
}

func (m *Manager) Unsubscribe(c chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.subs, c)
}

// notify wakes every subscriber; a wake-up already pending covers this one.
func (m *Manager) notify() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for c := range m.subs {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}

func (m *Manager) List() []SessionView {
	m.mu.Lock()
	sessions := append([]*Session(nil), m.sessions...)
	m.mu.Unlock()
	views := make([]SessionView, 0, len(sessions))
	for _, s := range sessions {
		views = append(views, s.View())
	}
	return views
}

func (m *Manager) Get(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, errNotFound
}

// Create starts a new claude session in cwd.
func (m *Manager) Create(name, cwd string, args []string) (*Session, error) {
	s := &Session{ID: newID(), Name: name, Cwd: cwd, Args: args, CreatedAt: time.Now()}
	return s, m.add(s, args)
}

// Takeover continues a claude conversation that ran somewhere else, here.
func (m *Manager) Takeover(claudeSessionID, name, cwd string) (*Session, error) {
	s := &Session{ID: newID(), Name: name, Cwd: cwd, ClaudeSessionID: claudeSessionID, CreatedAt: time.Now()}
	return s, m.add(s, []string{"--resume", claudeSessionID})
}

func (m *Manager) add(s *Session, args []string) error {
	if st, err := os.Stat(s.Cwd); err != nil || !st.IsDir() {
		return fmt.Errorf("%q is not a directory", s.Cwd)
	}
	if s.Name == "" {
		s.Name = filepath.Base(s.Cwd)
	}
	if err := m.run(s, args); err != nil {
		return err
	}
	m.mu.Lock()
	m.sessions = append(m.sessions, s)
	m.mu.Unlock()
	m.save()
	m.notify()
	return nil
}

// Resume restarts a session that exited or was stopped by a server restart,
// continuing its claude conversation.
func (m *Manager) Resume(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	if s.running() {
		return errRunning
	}
	if s.ClaudeSessionID == "" {
		return errNoResume
	}
	if err := m.run(s, append([]string{"--resume", s.ClaudeSessionID}, s.Args...)); err != nil {
		return err
	}
	m.notify()
	return nil
}

func (m *Manager) run(s *Session, args []string) error {
	claudeArgs := append([]string{"--settings", m.hooksFile, "--name", s.Name}, args...)
	env := append(childEnv(), "CLAUDE_MUX_ID="+s.ID, "TERM=xterm-256color", "COLORTERM=truecolor")
	return s.start(claudeArgs, env, func() {
		s.setStatus(StatusExited)
		m.notify()
	})
}

// sessionMarkers are set by a running Claude Code for its own children. A
// server started from inside a claude session must not pass them on: the
// child would take itself for a subagent and stop saving its transcript.
var sessionMarkers = map[string]bool{
	"CLAUDECODE": true, "CLAUDE_PID": true, "CLAUDE_EFFORT": true,
	"CLAUDE_CODE_ENTRYPOINT": true, "CLAUDE_CODE_EXECPATH": true,
	"CLAUDE_CODE_SESSION_ID": true, "CLAUDE_CODE_CHILD_SESSION": true, "CLAUDE_CODE_SESSION_ATTENDED": true,
	"CLAUDE_CODE_MESSAGING_SOCKET": true, "CLAUDE_CODE_MESSAGING_TOKEN": true,
}

func childEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !sessionMarkers[k] {
			env = append(env, kv)
		}
	}
	return env
}

// WatchStartup flags sessions that sit silent before their first hook: claude
// is waiting on a prompt it shows before hooks run (trusting the folder, a
// login), so the session needs you.
func (m *Manager) WatchStartup(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		m.mu.Lock()
		sessions := append([]*Session(nil), m.sessions...)
		m.mu.Unlock()
		for _, s := range sessions {
			s.mu.Lock()
			stuck := s.status == StatusStarting && s.ptmx != nil && time.Since(s.lastOutput) > 2*time.Second
			s.mu.Unlock()
			if stuck && s.setStatus(StatusNeedsYou) {
				m.notify()
			}
		}
	}
}

func (m *Manager) Rename(id, name string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.Name = name
	s.mu.Unlock()
	m.save()
	m.notify()
	return nil
}

// FollowClaudeName gives the tab of the session run by pid the name claude
// reports for it, when that name changed since the last report: /rename in
// the session renames the tab, while a rename made in the page stays until
// claude's name changes again.
func (m *Manager) FollowClaudeName(pid int, name string) {
	m.mu.Lock()
	sessions := append([]*Session(nil), m.sessions...)
	m.mu.Unlock()
	for _, s := range sessions {
		s.mu.Lock()
		if s.cmd == nil || s.cmd.Process == nil || s.cmd.Process.Pid != pid || name == "" || name == s.claudeName {
			s.mu.Unlock()
			continue
		}
		s.claudeName = name
		renamed := s.Name != name
		s.Name = name
		s.mu.Unlock()
		if renamed {
			m.save()
			m.notify()
		}
		return
	}
}

func (m *Manager) MarkSeen(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	changed := s.unseen
	s.unseen = false
	s.mu.Unlock()
	if changed {
		m.notify()
	}
	return nil
}

// Kill ends the session's process; the session stays listed, resumable.
func (m *Manager) Kill(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.kill()
	return nil
}

// Handoff moves a session out to a Terminal.app window: it ends the process
// here, waits for it to exit so the two never write one transcript, resumes
// the conversation in a new window, and forgets the tab.
func (m *Manager) Handoff(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	sid, name, cwd, args := s.ClaudeSessionID, s.Name, s.Cwd, s.Args
	s.mu.Unlock()
	if _, err := transcriptPath(sid); sid == "" || err != nil {
		return errors.New("nothing to hand off yet: the conversation has no messages")
	}
	s.kill()
	for deadline := time.Now().Add(6 * time.Second); s.running(); {
		if time.Now().After(deadline) {
			return errors.New("the session didn't exit; try again")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := OpenTerminal(cwd, append([]string{"claude", "--resume", sid, "--name", name}, args...)); err != nil {
		return err
	}
	return m.Remove(id)
}

// Reorder puts the sessions in the order of ids. Sessions it doesn't name
// keep their relative order after the named ones.
func (m *Manager) Reorder(ids []string) {
	m.mu.Lock()
	rank := map[string]int{}
	for i, id := range ids {
		rank[id] = i
	}
	slices.SortStableFunc(m.sessions, func(a, b *Session) int {
		ra, oka := rank[a.ID]
		rb, okb := rank[b.ID]
		switch {
		case oka && okb:
			return ra - rb
		case oka:
			return -1
		case okb:
			return 1
		}
		return 0
	})
	m.mu.Unlock()
	m.save()
	m.notify()
}

// Remove kills the session if it runs and forgets it.
func (m *Manager) Remove(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.kill()
	m.mu.Lock()
	for i, x := range m.sessions {
		if x == s {
			m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	m.save()
	m.notify()
	return nil
}

// Shutdown hangs up every running session.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	sessions := append([]*Session(nil), m.sessions...)
	m.mu.Unlock()
	for _, s := range sessions {
		s.kill()
	}
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
