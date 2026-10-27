//go:build !windows

package backend

import (
	"testing"
)

func TestFollowName(t *testing.T) {
	proc, err := startPty("sleep", []string{"5"}, nil, t.TempDir(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proc.cmd.Process.Kill() }()
	s := &Session{ID: "a", Name: "ops", proc: proc}
	m := &Manager{stateFile: t.TempDir() + "/backend.json", sessions: []*Session{s}}
	pid := proc.Pid()

	m.followName(pid, "ops")
	if err := m.Rename("a", "watch"); err != nil {
		t.Fatal(err)
	}
	// claude still reports the name it started with: the page's stays.
	m.followName(pid, "ops")
	if s.Name != "watch" {
		t.Errorf("renamed in the page, then %q", s.Name)
	}
	// /rename in the tab: claude reports a new name, which the tab takes.
	m.followName(pid, "deploys")
	if s.Name != "deploys" {
		t.Errorf("renamed by claude, got %q", s.Name)
	}
	m.followName(pid+1, "other")
	if s.Name != "deploys" {
		t.Errorf("another process's name taken: %q", s.Name)
	}
}
