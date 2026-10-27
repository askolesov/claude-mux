package backend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	errNotFound = errors.New("session not found")
	errRunning  = errors.New("session is still running")
)

// Manager owns the tabs: one claude each, in the order and groups of the
// sidebar. It keeps them in the state file and tells subscribers when
// anything changed. A tab lives until it is closed.
type Manager struct {
	stateFile string
	hooksFile string
	hookURL   string // the hook port's base, ".../hook/<token>"; a session adds its id

	mu       sync.Mutex
	claude   Claude
	sessions []*Session
	subs     map[chan struct{}]struct{}
}

// State is the state file.
type State struct {
	Sessions []*Session `json:"sessions"`
}

// NewManager loads the tabs and starts every one of them, continuing each
// tab's conversation.
func NewManager(stateDir, hookURL, exe string) (*Manager, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	m := &Manager{
		stateFile: filepath.Join(stateDir, "state.json"),
		hooksFile: filepath.Join(stateDir, "hooks.json"),
		hookURL:   hookURL,
		subs:      map[chan struct{}]struct{}{},
	}
	if err := writeHookSettings(m.hooksFile, exe); err != nil {
		return nil, err
	}
	if err := m.load(stateDir); err != nil {
		return nil, err
	}
	m.claude = findClaude()
	for _, s := range m.List() {
		if err := m.Start(s.ID); err != nil {
			fmt.Fprintf(os.Stderr, "start %s: %v\n", s.Name, err)
		}
	}
	return m, nil
}

func (m *Manager) load(stateDir string) error {
	data, err := os.ReadFile(m.stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return m.migrate(stateDir)
	}
	if err != nil {
		return err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("%s: %w", m.stateFile, err)
	}
	m.sessions = st.Sessions
	for _, s := range m.sessions {
		s.status, s.statusSince = StatusStopped, time.Now()
	}
	return nil
}

// migrate builds the state file from the two files of earlier versions: the
// tabs (sessions.json) and the saved conversations (favourites.json), whose
// restore messages become the tabs' startup messages. A saved conversation
// no tab holds is dropped, not opened: it may run in a terminal, and a
// second claude on it would fork its transcript. The old files are kept as
// *.migrated.
func (m *Manager) migrate(stateDir string) error {
	var tabs []*Session
	var saved []struct {
		ClaudeSessionID string `json:"claudeSessionId"`
		OnRestore       string `json:"onRestore"`
	}
	old := map[string]any{"sessions.json": &tabs, "favourites.json": &saved}
	for name, v := range old {
		if data, err := os.ReadFile(filepath.Join(stateDir, name)); err == nil {
			if err := json.Unmarshal(data, v); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	for _, f := range saved {
		for _, t := range tabs {
			if f.ClaudeSessionID != "" && t.ClaudeSessionID == f.ClaudeSessionID {
				t.StartupMessage = f.OnRestore
			}
		}
	}
	m.sessions = tabs
	for _, s := range m.sessions {
		s.status, s.statusSince = StatusStopped, time.Now()
	}
	m.groupTogether()
	m.save()
	for name := range old {
		_ = os.Rename(filepath.Join(stateDir, name), filepath.Join(stateDir, name+".migrated"))
	}
	return nil
}

func (m *Manager) save() {
	m.mu.Lock()
	data, err := json.MarshalIndent(State{Sessions: m.sessions}, "", "  ")
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

// Create opens a tab with a new claude in cwd.
func (m *Manager) Create(name, group, cwd, startupMessage string, args []string) (*Session, error) {
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", cwd)
	}
	if name == "" {
		name = filepath.Base(cwd)
	}
	s := &Session{ID: newID(), Name: name, Group: group, Cwd: cwd, Args: args, StartupMessage: startupMessage}
	m.mu.Lock()
	m.sessions = append(m.sessions, s)
	m.groupTogether()
	m.mu.Unlock()
	if err := m.Start(s.ID); err != nil {
		_ = m.Close(s.ID)
		return nil, err
	}
	m.save()
	return s, nil
}

// Start runs the tab's claude: it continues the tab's conversation, if it
// has one yet, and sends the startup message as the first prompt.
func (m *Manager) Start(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	if s.running() {
		return errRunning
	}
	claude := m.claudePath()
	if claude == "" {
		return errNoClaude
	}
	s.mu.Lock()
	name, sid, startupMessage := s.Name, s.ClaudeSessionID, s.StartupMessage
	args := append([]string{"--settings", m.hooksFile, "--name", name}, s.Args...)
	s.mu.Unlock()
	if hasConversation(sid) {
		args = append([]string{"--resume", sid}, args...)
	}
	if strings.TrimSpace(startupMessage) != "" {
		args = append(args, "--", startupMessage) // after "--", a message starting with "-" stays a prompt
	}
	env := append(childEnv(), "CLAUDE_MUX_ID="+s.ID, "CLAUDE_MUX_HOOK="+m.hookURL+"/"+s.ID, "TERM=xterm-256color", "COLORTERM=truecolor")
	if err := s.start(claude, args, env, func() {
		s.setStatus(StatusExited)
		m.notify()
	}); err != nil {
		return err
	}
	m.notify()
	return nil
}

// hasConversation reports whether claude has a transcript for the session:
// a tab where nothing was said yet has nothing to resume.
func hasConversation(sessionID string) bool {
	home, err := os.UserHomeDir()
	if sessionID == "" || err != nil || strings.ContainsAny(sessionID, `/\`) {
		return false
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", sessionID+".jsonl"))
	return len(matches) > 0
}

// sessionMarkers are set by a running Claude Code for its own children. A
// backend started from inside a claude session must not pass them on: the
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
			stuck := s.status == StatusStarting && s.proc != nil && time.Since(s.lastOutput) > 2*time.Second
			s.mu.Unlock()
			if stuck && s.setStatus(StatusNeedsYou) {
				m.notify()
			}
		}
	}
}

// PollNames follows the names claude reports for the tabs' processes in
// `claude agents --json`: claude owns the name, and /rename typed in a tab
// renames the tab. The name is passed back as --name on the next start.
func (m *Manager) PollNames(ctx context.Context, every time.Duration) {
	for {
		m.followNames(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (m *Manager) followNames(ctx context.Context) {
	claude := m.claudePath()
	if claude == "" {
		return
	}
	out, err := exec.CommandContext(ctx, claude, "agents", "--json").Output()
	if err != nil {
		return
	}
	var agents []struct {
		PID  int    `json:"pid"`
		Name string `json:"name"`
	}
	if json.Unmarshal(out, &agents) != nil {
		return
	}
	for _, a := range agents {
		m.followName(a.PID, a.Name)
	}
}

func (m *Manager) followName(pid int, name string) {
	m.mu.Lock()
	sessions := append([]*Session(nil), m.sessions...)
	m.mu.Unlock()
	for _, s := range sessions {
		s.mu.Lock()
		changed := s.proc != nil && s.proc.Pid() == pid && name != "" && name != s.Name
		if changed {
			s.Name = name
		}
		s.mu.Unlock()
		if changed {
			m.save()
			m.notify()
			return
		}
	}
}

// SetStartupMessage sets the prompt the tab's claude gets whenever it
// starts; "" means none.
func (m *Manager) SetStartupMessage(id, msg string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.StartupMessage = strings.TrimSpace(msg)
	s.mu.Unlock()
	m.save()
	m.notify()
	return nil
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

// SetGroup moves a tab into group, or out of any with "".
func (m *Manager) SetGroup(id, group string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	s.Group = strings.TrimSpace(group)
	m.groupTogether()
	m.mu.Unlock()
	m.save()
	m.notify()
	return nil
}

// RenameGroup renames a group on every tab; renaming to "" dissolves it,
// and renaming to an existing group merges the two.
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
	m.groupTogether()
	m.mu.Unlock()
	m.save()
	m.notify()
	return nil
}

// Close ends the tab's claude and removes the tab.
func (m *Manager) Close(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.kill()
	m.mu.Lock()
	m.sessions = slices.DeleteFunc(m.sessions, func(x *Session) bool { return x == s })
	m.mu.Unlock()
	m.save()
	m.notify()
	return nil
}

// Shutdown hangs up every session and waits for them to exit: the next
// start resuming a conversation whose old process still runs would fork its
// transcript. The tabs stay in the state file.
func (m *Manager) Shutdown() {
	m.mu.Lock()
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
