//go:build !windows

package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTerminal starts a terminal tab running sh, with cwd reads unthrottled.
func newTerminal(t *testing.T) (*Manager, *Session) {
	t.Helper()
	shellOverride = "/bin/sh"
	cwdEvery = 0
	t.Cleanup(func() { shellOverride, cwdEvery = "", defaultCwdEvery })
	m := newTestManager(t)
	s, err := m.Create(KindTerminal, "sh", "", "", "", ResumeOff, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return m, s
}

func TestTerminalStatus(t *testing.T) {
	m, s := newTerminal(t)
	s.write([]byte("sleep 1\n"))
	waitFor(t, "running", func() bool { m.checkTerminals(); return s.View().Status == StatusWorking })
	waitFor(t, "finished", func() bool { m.checkTerminals(); return s.View().Status == StatusReady })
	if !s.View().Unseen {
		t.Error("a finished command isn't marked unseen")
	}
}

func TestTerminalCwdFollowsShell(t *testing.T) {
	m, s := newTerminal(t)
	want, _ := filepath.EvalSymlinks(os.TempDir())
	s.write([]byte("cd " + want + "\n"))
	waitFor(t, "the cd", func() bool {
		m.checkTerminals()
		got, _ := filepath.EvalSymlinks(s.View().Cwd)
		return got == want
	})
	s.kill()
	waitFor(t, "the exit", func() bool { return !s.running() })
	if err := m.Start(s.ID); err != nil {
		t.Fatal(err)
	}
	s.write([]byte("pwd\n"))
	waitFor(t, "the new shell in the same folder", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return strings.Contains(string(s.scrollback), want)
	})
}
