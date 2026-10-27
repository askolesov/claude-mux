package backend

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// ResumeMode says what a tab gets as its first prompt when its claude starts
// again (claude-mux restarted, Restart, Resume).
type ResumeMode string

const (
	ResumeAsDefault ResumeMode = ""       // the hub's ResumeDefault
	ResumeCustom    ResumeMode = "custom" // the tab's own message
	ResumeOff       ResumeMode = "off"    // nothing
)

func (r ResumeMode) valid() error {
	if r != ResumeAsDefault && r != ResumeCustom && r != ResumeOff {
		return fmt.Errorf("unknown resume mode %q", r)
	}
	return nil
}

// ResumeDefault is the message every tab gets when its claude starts again,
// unless the tab says otherwise. The hub keeps it, for all environments: it
// gives it to a backend at launch (Flag) and again on every change.
type ResumeDefault struct {
	Enabled bool   `json:"enabled"`
	Message string `json:"message"`
}

// Flag is the backend's -resume argument: base64url, so that it passes
// through any shell unquoted.
func (d ResumeDefault) Flag() string {
	data, _ := json.Marshal(d)
	return base64.RawURLEncoding.EncodeToString(data)
}

// ParseResumeFlag reads what Flag wrote.
func ParseResumeFlag(s string) (ResumeDefault, error) {
	var d ResumeDefault
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err == nil {
		err = json.Unmarshal(data, &d)
	}
	return d, err
}

func (m *Manager) SetResumeDefault(d ResumeDefault) {
	m.mu.Lock()
	m.resume = d
	m.mu.Unlock()
}

// resumeMessage is what a tab in the given mode gets when it starts again.
func (m *Manager) resumeMessage(mode ResumeMode, own string) string {
	switch mode {
	case ResumeCustom:
		return strings.TrimSpace(own)
	case ResumeOff:
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.resume.Enabled {
		return ""
	}
	return strings.TrimSpace(m.resume.Message)
}
