package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Favourite is a saved conversation: what it takes to start it again, here
// or in a Terminal window. It belongs to no tab and outlives every restart.
type Favourite struct {
	ClaudeSessionID string   `json:"claudeSessionId"`
	Name            string   `json:"name"`
	Group           string   `json:"group,omitempty"` // the group its tab joins when it starts here
	Cwd             string   `json:"cwd"`
	Args            []string `json:"args"`
	OnRestore       string   `json:"onRestore,omitempty"` // first prompt whenever it starts in a new process
}

func (m *Manager) loadFavourites() error {
	data, err := os.ReadFile(m.favFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &m.favourites); err != nil {
		return fmt.Errorf("%s: %w", m.favFile, err)
	}
	return nil
}

func (m *Manager) saveFavourites() {
	m.mu.Lock()
	data, err := json.MarshalIndent(m.favourites, "", "  ")
	m.mu.Unlock()
	if err == nil {
		err = os.WriteFile(m.favFile, data, 0o600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "save favourites:", err)
	}
}

// Favourites is never nil, so the page gets [] rather than null.
func (m *Manager) Favourites() []Favourite {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Favourite{}, m.favourites...)
}

func (m *Manager) Favourite(sid string) (Favourite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range m.favourites {
		if f.ClaudeSessionID == sid {
			return f, nil
		}
	}
	return Favourite{}, errNotFound
}

// SetFavourite saves f, replacing the favourite with the same session id.
func (m *Manager) SetFavourite(f Favourite) error {
	if f.ClaudeSessionID == "" {
		return errors.New("the session has no conversation yet")
	}
	m.mu.Lock()
	i := slices.IndexFunc(m.favourites, func(x Favourite) bool { return x.ClaudeSessionID == f.ClaudeSessionID })
	if i >= 0 {
		m.favourites[i] = f
	} else {
		m.favourites = append(m.favourites, f)
	}
	m.mu.Unlock()
	m.saveFavourites()
	m.notify()
	return nil
}

// renameFavourite keeps a saved conversation's name in step with its tab.
func (m *Manager) renameFavourite(sid, name string) {
	m.mu.Lock()
	i := slices.IndexFunc(m.favourites, func(f Favourite) bool { return f.ClaudeSessionID == sid })
	changed := i >= 0 && m.favourites[i].Name != name
	if changed {
		m.favourites[i].Name = name
	}
	m.mu.Unlock()
	if changed {
		m.saveFavourites()
	}
}

// setFavouriteGroup keeps a saved conversation's group in step with its tab.
func (m *Manager) setFavouriteGroup(sid, group string) {
	m.mu.Lock()
	i := slices.IndexFunc(m.favourites, func(f Favourite) bool { return f.ClaudeSessionID == sid })
	changed := i >= 0 && m.favourites[i].Group != group
	if changed {
		m.favourites[i].Group = group
	}
	m.mu.Unlock()
	if changed {
		m.saveFavourites()
	}
}

func (m *Manager) Unfavourite(sid string) {
	m.mu.Lock()
	m.favourites = slices.DeleteFunc(m.favourites, func(f Favourite) bool { return f.ClaudeSessionID == sid })
	m.mu.Unlock()
	m.saveFavourites()
	m.notify()
}

// restorePrompt is the saved conversation's restore message as claude's
// first-prompt argument, sent whenever the conversation starts in a new
// process: background tasks of the old one (a watcher) are gone and the
// message can bring them back. After "--", so a message starting with "-"
// stays a prompt.
func (m *Manager) restorePrompt(sid string) []string {
	f, err := m.Favourite(sid)
	if err != nil || strings.TrimSpace(f.OnRestore) == "" {
		return nil
	}
	return []string{"--", f.OnRestore}
}

// StartFavourite continues a saved conversation in a new tab.
func (m *Manager) StartFavourite(sid string) (*Session, error) {
	f, err := m.Favourite(sid)
	if err != nil {
		return nil, err
	}
	return m.Takeover(f.ClaudeSessionID, f.Name, f.Cwd, f.Args)
}

// FavouriteInTerminal continues a saved conversation in a Terminal window.
func (m *Manager) FavouriteInTerminal(sid string) error {
	f, err := m.Favourite(sid)
	if err != nil {
		return err
	}
	args := append([]string{"claude", "--resume", f.ClaudeSessionID, "--name", f.Name}, f.Args...)
	return OpenTerminal(f.Cwd, append(args, m.restorePrompt(f.ClaudeSessionID)...))
}
