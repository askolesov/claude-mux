//go:build !windows

package backend

import (
	"strings"
	"testing"
	"time"
)

// catTab is a tab of kind whose process is cat, which echoes what it is
// typed, with status st.
func catTab(t *testing.T, kind Kind, st Status) (*Manager, *Session) {
	t.Helper()
	m := newTestManager(t)
	s := &Session{ID: "t", Kind: kind, Name: "old", Cwd: t.TempDir()}
	m.sessions = []*Session{s}
	if err := s.start("/bin/cat", nil, nil, func() {}); err != nil {
		t.Fatal(err)
	}
	s.setStatus(st)
	return m, s
}

func typed(s *Session) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.scrollback)
}

func TestRenameClaudeReadyTypesCommand(t *testing.T) {
	m, s := catTab(t, KindClaude, StatusReady)
	if err := m.Rename("t", " new name "); err != nil {
		t.Fatal(err)
	}
	if s.View().Name != "new name" {
		t.Errorf("name %q", s.View().Name)
	}
	waitFor(t, "/rename typed", func() bool { return strings.Contains(typed(s), "/rename new name") })
}

func TestRenameClaudeWorkingWaits(t *testing.T) {
	m, s := catTab(t, KindClaude, StatusWorking)
	if err := m.Rename("t", "new name"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(typed(s), "/rename") {
		t.Fatal("typed into a working tab")
	}
	_ = m.ApplyHook("t", HookPayload{Event: "Stop"})
	waitFor(t, "/rename typed on ready", func() bool { return strings.Contains(typed(s), "/rename new name") })
}

func TestRenameNotRevertedWhilePending(t *testing.T) {
	m, s := catTab(t, KindClaude, StatusWorking)
	_ = m.Rename("t", "new name")
	m.followName(s.proc.Pid(), "old")
	if s.View().Name != "new name" {
		t.Errorf("claude's old name came back: %q", s.View().Name)
	}
}

func TestRenameCodexStoresOnly(t *testing.T) {
	m, s := catTab(t, KindCodex, StatusReady)
	if err := m.Rename("t", "new name"); err != nil || s.View().Name != "new name" {
		t.Fatalf("%v, name %q", err, s.View().Name)
	}
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(typed(s), "rename") {
		t.Error("typed into a codex tab")
	}
}

func TestRenameRejectsEmptyAndMultiline(t *testing.T) {
	m, _ := catTab(t, KindTerminal, StatusIdle)
	for _, n := range []string{"  ", "a\nb"} {
		if err := m.Rename("t", n); err == nil {
			t.Errorf("took %q", n)
		}
	}
}
