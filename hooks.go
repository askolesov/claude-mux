package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// hookEvents are the Claude Code hook events that move a session's status.
var hookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop", "SessionEnd"}

// writeHookSettings writes the settings file passed to every claude the
// manager starts: each hook event posts its payload back to the server.
// A server that is down just drops the event.
func writeHookSettings(path, endpoint string) error {
	cmd := fmt.Sprintf(`curl -s -m 2 -X POST -H 'Content-Type: application/json' --data-binary @- "%s/$CLAUDE_MUX_ID" >/dev/null 2>&1 || true`, endpoint)
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

// HookPayload is the part of a hook's stdin the server reads.
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
		changedID := s.ClaudeSessionID != p.SessionID
		s.ClaudeSessionID = p.SessionID
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
		m.notify()
	}
	return nil
}
