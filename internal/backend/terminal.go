package backend

import (
	"context"
	"time"
)

// defaultCwdEvery is how often a terminal's folder is read at most.
const defaultCwdEvery = 5 * time.Second

var cwdEvery = defaultCwdEvery

// WatchTerminals follows, every second, what the terminal tabs run. The
// shell itself needs no help: whatever is in the terminal's foreground is
// running, so a tab whose foreground isn't its shell is working, and turns
// ready when the shell is back. Where that can't be read (Windows), a
// terminal stays idle.
func (m *Manager) WatchTerminals(ctx context.Context) {
	every(ctx, time.Second, m.checkTerminals)
}

func (m *Manager) checkTerminals() {
	for _, s := range m.all() {
		if s.Kind != KindTerminal {
			continue
		}
		s.mu.Lock()
		proc, st := s.proc, s.status
		s.mu.Unlock()
		if proc == nil {
			continue
		}
		pgid, ok := proc.Foreground()
		if !ok {
			continue
		}
		atPrompt := pgid == proc.Pid()
		changed := false
		switch {
		case !atPrompt:
			changed = s.setStatus(StatusWorking)
		case st == StatusWorking:
			changed = s.setStatus(StatusReady)
		}
		if atPrompt && m.followCwd(s, proc.Pid()) {
			changed = true
		}
		if changed {
			m.changes.Notify()
		}
	}
}

// followCwd keeps the tab's folder where its shell is, at most every
// cwdEvery, so that a new shell starts there. It reports a change.
func (m *Manager) followCwd(s *Session, pid int) bool {
	s.mu.Lock()
	due := time.Since(s.cwdAt) >= cwdEvery
	if due {
		s.cwdAt = time.Now()
	}
	s.mu.Unlock()
	if !due {
		return false
	}
	cwd, err := procCwd(pid)
	if err != nil || cwd == "" {
		return false
	}
	s.mu.Lock()
	changed := cwd != s.Cwd
	s.Cwd = cwd
	s.mu.Unlock()
	if changed {
		m.save()
	}
	return changed
}
