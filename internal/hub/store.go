package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/askolesov/claude-mux/internal/backend"
	"github.com/askolesov/claude-mux/internal/migrate"
)

// hubState is hub.json, everything the hub keeps: its settings, the
// environments in sidebar order (local among them), and what the update
// check last found. It is at version len(hubSteps(…)), migrations.go.
type hubState struct {
	Version      int         `json:"version"`
	Settings     Settings    `json:"settings"`
	Environments []EnvConfig `json:"environments"`
	Update       updateState `json:"update"`
}

// Settings are the hub's settings, set in the page.
type Settings struct {
	CheckUpdates bool                  `json:"checkUpdates"`
	EveryDays    int                   `json:"everyDays"`
	Resume       backend.ResumeDefault `json:"resume"`         // what every tab gets when it starts again, unless it says otherwise
	Page         json.RawMessage       `json:"page,omitempty"` // the page's own settings, which the hub keeps without reading
}

func (s Settings) validate() error {
	if s.EveryDays < 1 || s.EveryDays > 365 {
		return errors.New("check every 1 to 365 days")
	}
	return nil
}

const defaultResumeMessage = "claude-mux restarted this session: its claude process started again, so " +
	"anything that was running in it (background tasks, watchers, servers) has stopped. " +
	"If you were in the middle of something, carry on; restart any background work that should be running."

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
	st.s.Settings = Settings{CheckUpdates: true, EveryDays: 1, Resume: backend.ResumeDefault{Message: defaultResumeMessage}}
	steps := hubSteps(dir)
	data, upgraded, err := migrate.Load(st.path, steps)
	if err == nil && data == nil {
		// A first start, or one from before v0.4 with only environments.json,
		// which step 1 reads.
		data, upgraded, err = migrate.Upgrade([]byte("{}"), steps)
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &st.s); err != nil {
		return nil, fmt.Errorf("%s: %w", st.path, err)
	}
	st.s.Version = len(steps)
	if !slices.ContainsFunc(st.s.Environments, func(e EnvConfig) bool { return e.Kind == KindLocal }) {
		st.s.Environments = append([]EnvConfig{{Name: "local", Kind: KindLocal}}, st.s.Environments...)
	}
	if upgraded {
		st.change(func(*hubState) {})
		_ = os.Rename(filepath.Join(dir, "environments.json"), filepath.Join(dir, "environments.json.bak"))
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
	data, err := json.MarshalIndent(st.s, "", "  ")
	if err == nil {
		err = os.WriteFile(st.path, data, 0o600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "save hub.json:", err)
	}
}
