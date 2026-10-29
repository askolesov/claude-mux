package backend

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// hookEvents are the Claude Code hook events that move a session's status
// (codexHookEvents are Codex's).
var hookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop", "SessionEnd"}

// writeHookSettings writes the settings file passed to every claude the
// manager starts: each hook event runs this backend's binary as `hook`,
// which posts the payload back to the backend (see Hook).
func writeHookSettings(path, exe string) error {
	// Claude runs hooks with sh, Git Bash on Windows, which takes C:/… paths.
	cmd := shellQuote(filepath.ToSlash(exe)) + " hook"
	hooks := map[string]any{}
	for _, e := range hookEvents {
		hooks[e] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}}}
	}
	data, err := json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// shellQuote wraps s in single quotes. A quote inside becomes '\” (close,
// escaped quote, reopen), which sh, zsh and fish all read the same way.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Hook is the hook command: it posts the hook's payload, read on stdin, to
// the address its backend gave the session in CLAUDE_MUX_HOOK. It never
// fails the hook: a backend that is gone just misses the event.
func Hook() {
	url := os.Getenv("CLAUDE_MUX_HOOK")
	payload, err := io.ReadAll(os.Stdin)
	if url == "" || err != nil {
		return
	}
	client := &http.Client{Timeout: 2 * time.Second}
	if res, err := client.Post(url, "application/json", bytes.NewReader(payload)); err == nil {
		_ = res.Body.Close()
	}
}

// HookPayload is the part of a hook's stdin the backend reads.
type HookPayload struct {
	Event            string `json:"hook_event_name"`
	SessionID        string `json:"session_id"`
	Source           string `json:"source"` // SessionStart's: startup, resume, clear…
	NotificationType string `json:"notification_type"`
}

// ApplyHook moves a session's status for one hook event. The hook address
// is inherited, so an agent the tab's own agent runs (a codex in a claude
// tab) posts here too: once the tab has its session, events of another one
// are ignored. The first SessionStart after a start names the session (a
// resumed one may come back under a new id), and so does one from /clear.
func (m *Manager) ApplyHook(id string, p HookPayload) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	kind := s.Kind
	if kind == KindTerminal {
		s.mu.Unlock()
		return nil
	}
	changedID := false
	if p.Event == "SessionStart" && (!s.started || p.Source == "clear") {
		s.started = true
		changedID = p.SessionID != "" && s.AgentSessionID != p.SessionID
		if changedID {
			s.AgentSessionID = p.SessionID
		}
	} else if s.AgentSessionID != "" && p.SessionID != "" && p.SessionID != s.AgentSessionID {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	if changedID {
		m.save()
	}
	st := hookStatus(kind, p)
	if st != "" && s.setStatus(st) {
		m.changes.Notify()
	}
	if st == StatusReady {
		m.sendRename(s)
	}
	return nil
}

// hookStatus is the status a hook event moves a tab of kind to, or "" for
// none.
func hookStatus(kind Kind, p HookPayload) Status {
	switch p.Event {
	case "SessionStart", "Stop", "Interrupt":
		return StatusReady
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		return StatusWorking
	case "PermissionRequest":
		return StatusNeedsYou
	case "Notification":
		if p.NotificationType == "permission_prompt" || p.NotificationType == "elicitation_dialog" {
			return StatusNeedsYou
		}
	case "SessionEnd":
		// Codex also ends a session after 30 idle minutes, with its process
		// still running; the process's exit says exited.
		if kind == KindClaude {
			return StatusExited
		}
	}
	return ""
}
