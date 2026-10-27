package hub

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// hubState is hub.json, everything the hub keeps: its settings, the
// environments in sidebar order (local among them), and what the update
// check last found.
type hubState struct {
	Settings     Settings    `json:"settings"`
	Environments []EnvConfig `json:"environments"`
	Update       updateState `json:"update"`
}

type updateState struct {
	LastCheck time.Time `json:"lastCheck,omitzero"`
	Skip      string    `json:"skip,omitempty"` // a version not to offer again
}

// store keeps hub.json, written whole on every change.
type store struct {
	path string

	mu sync.Mutex
	s  hubState
}

func openStore(dir string) (*store, error) {
	st := &store{path: filepath.Join(dir, "hub.json")}
	st.s.Settings = Settings{CheckUpdates: true, EveryDays: 1, Resume: ResumeDefault{Message: defaultResumeMessage}}
	migrated := false
	data, err := os.ReadFile(st.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &st.s); err != nil {
			return nil, fmt.Errorf("%s: %w", st.path, err)
		}
		if !bytes.Contains(data, []byte(`"settings"`)) { // v0.3's: the update check's fields alone
			var old struct {
				Settings
				updateState
			}
			if json.Unmarshal(data, &old) == nil && old.EveryDays > 0 {
				st.s.Settings, st.s.Update = old.Settings, old.updateState
			}
			migrated = true
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	// Before v0.4 the environments were in environments.json.
	oldEnvs := filepath.Join(dir, "environments.json")
	if data, err := os.ReadFile(oldEnvs); err == nil {
		var envs []EnvConfig
		if err := json.Unmarshal(data, &envs); err != nil {
			return nil, fmt.Errorf("%s: %w", oldEnvs, err)
		}
		st.s.Environments = append(st.s.Environments, envs...)
		migrated = true
	}
	if !slices.ContainsFunc(st.s.Environments, func(e EnvConfig) bool { return e.Kind == "local" }) {
		st.s.Environments = append([]EnvConfig{{Name: "local", Kind: "local"}}, st.s.Environments...)
	}
	if migrated {
		st.mu.Lock()
		err := st.write()
		st.mu.Unlock()
		if err != nil {
			return nil, err
		}
		_ = os.Remove(oldEnvs)
	}
	return st, nil
}

// get is a copy of what hub.json holds.
func (st *store) get() hubState {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.s
	s.Environments = slices.Clone(s.Environments)
	return s
}

// change applies f and writes hub.json.
func (st *store) change(f func(*hubState)) {
	st.mu.Lock()
	defer st.mu.Unlock()
	f(&st.s)
	if err := st.write(); err != nil {
		fmt.Fprintln(os.Stderr, "save hub.json:", err)
	}
}

// write is called with mu held.
func (st *store) write() error {
	data, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(st.path, data, 0o600)
}
