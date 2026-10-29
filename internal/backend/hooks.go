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

// hookEvents are the Claude Code hook events that move a session's status.
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
	NotificationType string `json:"notification_type"`
}

// ApplyHook moves a session's status for one hook event.
func (m *Manager) ApplyHook(id string, p HookPayload) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	if p.SessionID != "" {
		s.mu.Lock()
		changedID := s.AgentSessionID != p.SessionID
		s.AgentSessionID = p.SessionID
		s.mu.Unlock()
		if changedID { // a new session, or /clear started a fresh one
			m.save()
		}
	}
	var st Status
	switch p.Event {
	case "SessionStart", "Stop":
		st = StatusReady
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		st = StatusWorking
	case "Notification":
		if p.NotificationType == "permission_prompt" || p.NotificationType == "elicitation_dialog" {
			st = StatusNeedsYou
		}
	case "SessionEnd":
		st = StatusExited
	}
	if st != "" && s.setStatus(st) {
		m.changes.Notify()
	}
	return nil
}
