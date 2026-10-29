package backend

import (
	"errors"
	"strings"
	"time"
)

// renameWait is how long a Claude tab's new name waits for claude to report
// it back; after that, claude's name wins.
const renameWait = 15 * time.Second

// Rename names a tab; the new name shows at once. A Claude tab's name is
// claude's too: Rename types /rename into the session, after clearing the
// input line (a half-typed prompt there is lost), once claude is ready for
// a prompt, and WatchNames reads the name back.
func (m *Manager) Rename(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "\r\n") {
		return errors.New("a tab's name is one line, not empty")
	}
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.Name = name
	if s.Kind == KindClaude {
		s.pendingName, s.renamedAt = name, time.Time{}
	}
	s.mu.Unlock()
	m.save()
	m.changes.Notify()
	m.sendRename(s)
	return nil
}

// sendRename types a Claude tab's pending /rename, if claude is ready for a
// prompt and it hasn't been typed yet. Ctrl-U clears the input line first.
func (m *Manager) sendRename(s *Session) {
	s.mu.Lock()
	name := s.pendingName
	due := name != "" && s.renamedAt.IsZero() && s.status == StatusReady && s.proc != nil
	if due {
		s.renamedAt = time.Now()
	}
	s.mu.Unlock()
	if due {
		s.write([]byte("\x15/rename " + name + "\r"))
	}
}
