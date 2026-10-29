//go:build !windows

package backend

import (
	"strings"
	"testing"
	"time"
)

// newTestManager is a manager with no saved state, its tabs in dir.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	m := &Manager{stateFile: dir + "/backend.json", defaultCwd: dir, hookURL: "http://127.0.0.1:1/hook/t", agents: map[Kind]Agent{}}
	t.Cleanup(m.Shutdown)
	return m
}

// waitFor polls cond for up to 5 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStartTerminal(t *testing.T) {
	shellOverride = "/bin/sh"
	t.Cleanup(func() { shellOverride = "" })
	m := newTestManager(t)
	s, err := m.Create(KindTerminal, "sh", "", "", "", ResumeCustom, "ignored", []string{"--x"})
	if err != nil {
		t.Fatal(err)
	}
	if s.View().Status != StatusIdle || s.Args != nil || s.Resume != ResumeOff {
		t.Errorf("a new terminal: %+v", s.View())
	}
	s.write([]byte("echo hook=[$CLAUDE_MUX_HOOK]\n"))
	waitFor(t, "the echo", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return strings.Contains(string(s.scrollback), "hook=[]")
	})
}
