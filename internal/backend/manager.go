package backend

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
	favFile      string
	hooksFile string
	hookURL   string // the hook port's base, ".../hook/<token>"; a session adds its id

	mu           sync.Mutex
	shuttingDown bool // claudes exiting now were hung up by Shutdown; they run again on the next start
	sessions     []*Session
	favourites []Favourite
	subs       map[chan struct{}]struct{}
}

// NewManager loads the favourites and brings back the tabs that were running
// when the backend last stopped: the hub quitting is a restart.
func NewManager(stateDir, hookURL, exe string) (*Manager, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	m := &Manager{
		stateFile:    filepath.Join(stateDir, "sessions.json"),
		favFile:      filepath.Join(stateDir, "favourites.json"),
		hooksFile:  filepath.Join(stateDir, "hooks.json"),
		hookURL:    hookURL,
		subs:       map[chan struct{}]struct{}{},
	}
	if err := writeHookSettings(m.hooksFile, exe); err != nil {
		return nil, err
	}
	if err := m.loadFavourites(); err != nil {
		return nil, err
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	for _, s := range m.List() {
		if err := m.Resume(s.ID); err != nil {
			fmt.Fprintf(os.Stderr, "resume %s: %v\n", s.Name, err)
		}
	}
	return m, nil
}

// load reads the tabs of the previous run and keeps the ones that were
// running when it stopped; NewManager resumes them.
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
	m.sessions = slices.DeleteFunc(m.sessions, func(s *Session) bool {
		_, noTranscript := transcriptPath(s.ClaudeSessionID)
		return !s.WasRunning || noTranscript != nil // nothing said yet: nothing to resume
	})
	for _, s := range m.sessions {
		s.WasRunning = false
		s.status, s.statusSince = StatusStopped, time.Now()
	}
	m.save()
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
func (m *Manager) Create(name, group, cwd string, args []string) (*Session, error) {
	s := &Session{ID: newID(), Name: name, Group: group, Cwd: cwd, Args: args, CreatedAt: time.Now()}
	return s, m.add(s, args)
}

// Takeover continues a claude conversation that ran somewhere else, here. A
// saved conversation's tab joins its group.
func (m *Manager) Takeover(claudeSessionID, name, cwd string, args []string) (*Session, error) {
	s := &Session{ID: newID(), Name: name, Cwd: cwd, Args: args, ClaudeSessionID: claudeSessionID, CreatedAt: time.Now()}
	if f, err := m.Favourite(claudeSessionID); err == nil {
		s.Group = f.Group
	}
	return s, m.add(s, append(append([]string{"--resume", claudeSessionID}, args...), m.restorePrompt(claudeSessionID)...))
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
	m.groupTogether()
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
	if err := m.run(s, append(append([]string{"--resume", s.ClaudeSessionID}, s.Args...), m.restorePrompt(s.ClaudeSessionID)...)); err != nil {
		return err
	}
	m.notify()
	return nil
}

func (m *Manager) run(s *Session, args []string) error {
	claudeArgs := append([]string{"--settings", m.hooksFile, "--name", s.Name}, args...)
	env := append(childEnv(), "CLAUDE_MUX_ID="+s.ID, "CLAUDE_MUX_HOOK="+m.hookURL+"/"+s.ID, "TERM=xterm-256color", "COLORTERM=truecolor")
	err := s.start(claudeArgs, env, func() {
		m.mu.Lock()
		shuttingDown := m.shuttingDown
		m.mu.Unlock()
		if !shuttingDown {
			s.mu.Lock()
			s.WasRunning = false
			s.mu.Unlock()
			m.save()
		}
		s.setStatus(StatusExited)
		m.notify()
	})
	if err != nil {
		return err
	}
	// Saved now, not at shutdown, so a backend that dies resumes it too.
	s.mu.Lock()
	s.WasRunning = true
	s.mu.Unlock()
	m.save()
	return nil
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

// Rename renames the session through claude: /rename typed into it, now if
// it waits for a prompt, else when it next does (typed into a turn or a
// permission prompt it would land in the wrong place). The tab follows the
// name claude then reports. A session that doesn't run keeps the name for
// its next start, which passes it as --name.
func (m *Manager) Rename(id, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("empty name")
	}
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	running, ready := s.ptmx != nil, s.status == StatusReady
	if running && !ready {
		s.pendingName = name
	}
	if !running {
		s.Name = name
	}
	sid := s.ClaudeSessionID
	s.mu.Unlock()
	switch {
	case running && ready:
		s.typeRename(name)
	case !running:
		m.renameFavourite(sid, name)
		m.save()
		m.notify()
	}
	return nil
}

// FollowClaudeName gives the tab of the session run by pid the name claude
// reports for it: claude's list is the source of truth for names.
func (m *Manager) FollowClaudeName(pid int, name string) {
	m.mu.Lock()
	sessions := append([]*Session(nil), m.sessions...)
	m.mu.Unlock()
	for _, s := range sessions {
		s.mu.Lock()
		if s.cmd == nil || s.cmd.Process == nil || s.cmd.Process.Pid != pid || name == "" || name == s.Name {
			s.mu.Unlock()
			continue
		}
		s.Name = name
		sid := s.ClaudeSessionID
		s.mu.Unlock()
		m.renameFavourite(sid, name)
		m.save()
		m.notify()
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
	if err := OpenTerminal(cwd, append(append([]string{"claude", "--resume", sid, "--name", name}, args...), m.restorePrompt(sid)...)); err != nil {
		return err
	}
	return m.Close(id)
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
	m.groupTogether()
	m.mu.Unlock()
	m.save()
	m.notify()
}

// groupTogether keeps each group's tabs in one run, where its first tab is;
// tabs without a group keep their places. The caller holds m.mu.
func (m *Manager) groupTogether() {
	first := map[string]int{}
	rank := make(map[*Session]int, len(m.sessions))
	for i, s := range m.sessions {
		if s.Group == "" {
			rank[s] = i
			continue
		}
		if _, ok := first[s.Group]; !ok {
			first[s.Group] = i
		}
		rank[s] = first[s.Group]
	}
	slices.SortStableFunc(m.sessions, func(a, b *Session) int { return rank[a] - rank[b] })
}

// SetGroup moves a tab into group, or out of any with "". Its saved
// conversation, if any, keeps the group for its next start.
func (m *Manager) SetGroup(id, group string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	group = strings.TrimSpace(group)
	m.mu.Lock()
	s.Group = group
	sid := s.ClaudeSessionID
	m.groupTogether()
	m.mu.Unlock()
	m.setFavouriteGroup(sid, group)
	m.save()
	m.notify()
	return nil
}

// RenameGroup renames a group on every tab and saved conversation; renaming
// to "" dissolves it, and renaming to an existing group merges the two.
func (m *Manager) RenameGroup(from, to string) error {
	if from == "" {
		return errors.New("no group to rename")
	}
	to = strings.TrimSpace(to)
	m.mu.Lock()
	for _, s := range m.sessions {
		if s.Group == from {
			s.Group = to
		}
	}
	for i := range m.favourites {
		if m.favourites[i].Group == from {
			m.favourites[i].Group = to
		}
	}
	m.groupTogether()
	m.mu.Unlock()
	m.save()
	m.saveFavourites()
	m.notify()
	return nil
}

// Close ends the session if it runs and removes its tab. A favourite stays
// in the favourites.
func (m *Manager) Close(id string) error {
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

// Shutdown hangs up every session, keeping WasRunning for the next start,
// and waits for them to exit: a restart resuming a conversation whose old
// process still runs would fork its transcript.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	m.shuttingDown = true
	sessions := append([]*Session(nil), m.sessions...)
	m.mu.Unlock()
	for _, s := range sessions {
		s.kill()
	}
	// kill escalates to SIGKILL after 3s, so this ends.
	for slices.ContainsFunc(sessions, (*Session).running) {
		time.Sleep(50 * time.Millisecond)
	}
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
