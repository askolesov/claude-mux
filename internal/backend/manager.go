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

	"github.com/askolesov/claude-mux/internal/migrate"
	"github.com/askolesov/claude-mux/internal/stream"
	"github.com/askolesov/claude-mux/internal/sysproc"
)

var (
	errNotFound = errors.New("no such tab or group")
	errRunning  = errors.New("the tab's claude is still running")
)

// Manager owns the tabs, one claude each, and the groups they sit in (see
// tree.go). It keeps them in backend.json and tells the page's event
// streams when anything changed. A tab lives until it is closed.
type Manager struct {
	stateFile string
	hooksFile string
	hookURL   string // the hook port's base, ".../hook/<token>"; a session adds its id

	changes stream.Notifier // wakes the page's event streams

	mu       sync.Mutex
	claude   Claude
	sessions []*Session
	groups   []*Group
	version  int // backend.json's
}

// State is backend.json, at version len(stateSteps(…)) (migrations.go).
type State struct {
	Version  int        `json:"version"`
	Sessions []*Session `json:"sessions"`
	Groups   []*Group   `json:"groups"`
}

// NewManager loads the tabs and starts every one of them, continuing each
// tab's conversation. old is the resume default from before v0.7, for
// upgrading backend.json.
func NewManager(stateDir, hookURL, exe string, old ResumeDefault) (*Manager, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	m := &Manager{
		stateFile: filepath.Join(stateDir, "backend.json"),
		hooksFile: filepath.Join(stateDir, "backend-hooks.json"),
		hookURL:   hookURL,
	}
	if err := writeHookSettings(m.hooksFile, exe); err != nil {
		return nil, err
	}
	if err := m.load(stateDir, stateSteps(old)); err != nil {
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

// load reads backend.json, upgrading it from an older version.
func (m *Manager) load(stateDir string, steps []migrate.Step) error {
	m.version = len(steps)
	data, upgraded, err := migrate.Load(m.stateFile, steps)
	if err == nil && data == nil {
		// Before v0.4 the file was state.json, and the hooks hooks.json.
		data, upgraded, err = migrate.Load(filepath.Join(stateDir, "state.json"), steps)
		if data != nil {
			defer func() {
				_ = os.Rename(filepath.Join(stateDir, "state.json"), filepath.Join(stateDir, "state.json.bak"))
				_ = os.Remove(filepath.Join(stateDir, "hooks.json"))
			}()
		}
	}
	if err != nil || data == nil {
		return err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("%s: %w", m.stateFile, err)
	}
	m.sessions, m.groups = st.Sessions, st.Groups
	for _, s := range m.sessions {
		s.status, s.statusSince = StatusStopped, time.Now()
	}
	if upgraded {
		m.save()
	}
	return nil
}

// save writes backend.json, with every tab locked while it is read. A write
// that fails is logged: the change holds in memory, and the next save
// writes it again.
func (m *Manager) save() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	if err := migrate.Save(m.stateFile, State{Version: m.version, Sessions: m.sessions, Groups: m.groups}); err != nil {
		fmt.Fprintln(os.Stderr, "saving backend.json:", err)
	}
}

// List is every tab, as the page sees it.
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

// Get finds a tab by its id.
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

// Create opens a tab with a new claude in cwd, in parent: after a child of
// it, or at its end.
func (m *Manager) Create(name, parent, after, cwd string, resume ResumeMode, resumeMessage string, args []string) (*Session, error) {
	if err := resume.valid(); err != nil {
		return nil, err
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", cwd)
	}
	if name == "" {
		name = filepath.Base(cwd)
	}
	s := &Session{ID: newID(), Name: name, Parent: parent, Cwd: cwd, Args: args, Resume: resume, ResumeMessage: strings.TrimSpace(resumeMessage)}
	m.mu.Lock()
	err := m.validParent(parent, "")
	if err == nil {
		s.Order, err = m.place(s.ID, parent, "", after)
	}
	if err == nil {
		m.sessions = append(m.sessions, s)
	}
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := m.Start(s.ID); err != nil {
		_ = m.Close(s.ID)
		return nil, err
	}
	m.save()
	return s, nil
}

// Start runs the tab's claude: it continues the tab's conversation, if it
// has one yet, and sends its resume message as the first prompt then.
func (m *Manager) Start(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	if s.running() {
		return errRunning
	}
	claude := m.Claude().Path
	if claude == "" {
		return errNoClaude
	}
	s.mu.Lock()
	name, sid, resume, own := s.Name, s.ClaudeSessionID, s.Resume, s.ResumeMessage
	args := append([]string{"--settings", m.hooksFile, "--name", name}, s.Args...)
	s.mu.Unlock()
	// Starting again (claude-mux restarted, Restart, Resume) continues the
	// conversation, and tells claude so: what ran in the old process,
	// background tasks and watchers, is gone.
	if hasConversation(sid) {
		args = append([]string{"--resume", sid}, args...)
		if msg := resumeMessage(resume, own); msg != "" {
			args = append(args, "--", msg) // after "--", a message starting with "-" stays a prompt
		}
	}
	// FORCE_HYPERLINK: claude marks its links as terminal hyperlinks (OSC 8),
	// which stay one link when claude breaks them over several lines.
	env := append(childEnv(), "CLAUDE_MUX_ID="+s.ID, "CLAUDE_MUX_HOOK="+m.hookURL+"/"+s.ID,
		"TERM=xterm-256color", "COLORTERM=truecolor", "FORCE_HYPERLINK=1")
	if err := s.start(claude, args, env, func() {
		s.mu.Lock()
		again := s.restarting
		s.restarting = false
		s.mu.Unlock()
		if again && m.Start(s.ID) == nil {
			return
		}
		s.setStatus(StatusExited)
		m.changes.Notify()
	}); err != nil {
		return err
	}
	m.changes.Notify()
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
				m.changes.Notify()
			}
		}
	}
}

// PollNames follows the names claude reports for the tabs' processes in
// `claude agents --json`: claude owns the name, and /rename typed in a tab
// is the only way to rename it. The name is passed back as --name on the
// next start.
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
	claude := m.Claude().Path
	if claude == "" {
		return
	}
	out, err := sysproc.Hide(exec.CommandContext(ctx, claude, "agents", "--json")).Output()
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
			m.changes.Notify()
			return
		}
	}
}

// Configure sets what a tab starts with: its directory, startup message and
// extra arguments, from its next start.
func (m *Manager) Configure(id, cwd string, resume ResumeMode, resumeMessage string, args []string) error {
	if err := resume.valid(); err != nil {
		return err
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return fmt.Errorf("%q is not a directory", cwd)
	}
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.Cwd, s.Resume, s.ResumeMessage, s.Args = cwd, resume, strings.TrimSpace(resumeMessage), args
	s.mu.Unlock()
	m.save()
	m.changes.Notify()
	m.refreshBranch(s)
	return nil
}

// Restart ends a tab's claude and starts it again, continuing its
// conversation; one that isn't running just starts.
func (m *Manager) Restart(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	if !s.running() {
		return m.Start(id)
	}
	s.mu.Lock()
	s.restarting = true
	s.mu.Unlock()
	s.kill()
	return nil
}

// WatchBranches keeps each tab's git branch current.
func (m *Manager) WatchBranches(ctx context.Context, every time.Duration) {
	for {
		m.mu.Lock()
		sessions := append([]*Session(nil), m.sessions...)
		m.mu.Unlock()
		for _, s := range sessions {
			m.refreshBranch(s)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (m *Manager) refreshBranch(s *Session) {
	s.mu.Lock()
	cwd := s.Cwd
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, _ := sysproc.Hide(exec.CommandContext(ctx, "git", "-C", cwd, "branch", "--show-current")).Output()
	branch := strings.TrimSpace(string(out))
	s.mu.Lock()
	changed := s.branch != branch
	s.branch = branch
	s.mu.Unlock()
	if changed {
		m.changes.Notify()
	}
}

// MarkSeen notes that the page has shown the tab since its last change.
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
		m.changes.Notify()
	}
	return nil
}

// Close ends the tab's claude and removes the tab, with the files dropped
// into it.
func (m *Manager) Close(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.kill()
	m.mu.Lock()
	m.sessions = slices.DeleteFunc(m.sessions, func(x *Session) bool { return x == s })
	m.mu.Unlock()
	if err := os.RemoveAll(m.filesDir(id)); err != nil {
		fmt.Fprintln(os.Stderr, "removing a closed tab's files:", err)
	}
	m.save()
	m.changes.Notify()
	return nil
}

// Claude is what the backend last found of claude here.
func (m *Manager) Claude() Claude {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.claude
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
