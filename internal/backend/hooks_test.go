package backend

import "testing"

// hookTab is a manager with one tab of kind, as if its process had just
// started, with status st and session id sid.
func hookTab(kind Kind, st Status, sid string) (*Manager, *Session) {
	s := &Session{ID: "t", Kind: kind, AgentSessionID: sid, status: st}
	return &Manager{sessions: []*Session{s}}, s
}

func TestApplyHookCodexStatuses(t *testing.T) {
	for _, c := range []struct {
		event string
		want  Status
	}{
		{"SessionStart", StatusReady},
		{"UserPromptSubmit", StatusWorking},
		{"PreToolUse", StatusWorking},
		{"PostToolUse", StatusWorking},
		{"PermissionRequest", StatusNeedsYou},
		{"Stop", StatusReady},
		{"Interrupt", StatusReady},
		{"SessionEnd", StatusIdle}, // unchanged: codex sends it after 30 min idle too
	} {
		m, s := hookTab(KindCodex, StatusIdle, "")
		if err := m.ApplyHook("t", HookPayload{Event: c.event, SessionID: "u1", Source: "startup"}); err != nil {
			t.Fatal(err)
		}
		if got := s.View().Status; got != c.want {
			t.Errorf("%s: %s, want %s", c.event, got, c.want)
		}
	}
}

func TestApplyHookFirstStartTakesAnyID(t *testing.T) {
	m, s := hookTab(KindClaude, StatusStarting, "old")
	_ = m.ApplyHook("t", HookPayload{Event: "SessionStart", SessionID: "new", Source: "resume"})
	if s.AgentSessionID != "new" {
		t.Errorf("id %q, want new", s.AgentSessionID)
	}
}

func TestApplyHookIgnoresOtherSession(t *testing.T) {
	m, s := hookTab(KindClaude, StatusStarting, "")
	_ = m.ApplyHook("t", HookPayload{Event: "SessionStart", SessionID: "a", Source: "startup"})
	_ = m.ApplyHook("t", HookPayload{Event: "UserPromptSubmit", SessionID: "a"})
	// A codex run inside the tab inherits its address.
	_ = m.ApplyHook("t", HookPayload{Event: "SessionStart", SessionID: "b", Source: "startup"})
	_ = m.ApplyHook("t", HookPayload{Event: "Stop", SessionID: "b"})
	if s.AgentSessionID != "a" || s.View().Status != StatusWorking {
		t.Errorf("another session moved the tab: id %q, %s", s.AgentSessionID, s.View().Status)
	}
	_ = m.ApplyHook("t", HookPayload{Event: "SessionStart", SessionID: "c", Source: "clear"})
	if s.AgentSessionID != "c" {
		t.Errorf("/clear's fresh session: id %q, want c", s.AgentSessionID)
	}
}

func TestApplyHookTerminalIgnored(t *testing.T) {
	m, s := hookTab(KindTerminal, StatusIdle, "")
	for _, e := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
		_ = m.ApplyHook("t", HookPayload{Event: e, SessionID: "x"})
	}
	if s.AgentSessionID != "" || s.View().Status != StatusIdle {
		t.Errorf("a terminal took a hook: %+v", s.View())
	}
}

// /resume inside a running tab switches it to another conversation: the tab
// follows it.
func TestApplyHookFollowsInSessionResume(t *testing.T) {
	m, s := hookTab(KindClaude, StatusStarting, "")
	_ = m.ApplyHook("t", HookPayload{Event: "SessionStart", SessionID: "a", Source: "startup"})
	_ = m.ApplyHook("t", HookPayload{Event: "SessionStart", SessionID: "b", Source: "resume"})
	_ = m.ApplyHook("t", HookPayload{Event: "UserPromptSubmit", SessionID: "b"})
	if s.AgentSessionID != "b" || s.View().Status != StatusWorking {
		t.Errorf("after /resume: id %q, %s", s.AgentSessionID, s.View().Status)
	}
}
