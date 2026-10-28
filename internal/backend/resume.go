package backend

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// ResumeMode says whether a tab has a startup message: a first prompt it
// gets whenever its claude starts again (claude-mux restarted, Restart,
// Resume). The text is kept while it is off, to turn it back on.
type ResumeMode string

const (
	ResumeCustom ResumeMode = "custom" // the tab's own message
	ResumeOff    ResumeMode = "off"    // no message of its own; the global one still comes
)

func (r ResumeMode) valid() error {
	if r != ResumeCustom && r != ResumeOff {
		return fmt.Errorf("unknown resume mode %q", r)
	}
	return nil
}

// StartupMessage is a first prompt and whether it is on. The page's
// Settings set one for every tab, the global message, which the hub gives a
// backend at launch (-global, Flag) and on every change (PUT
// /api/global-message). Before v0.7 the hub kept the same thing as the
// resume default; it still passes that (-resume), only so that a
// backend.json from then upgrades with each tab keeping the message it had
// (migrations.go).
type StartupMessage struct {
	Enabled bool   `json:"enabled"`
	Message string `json:"message"`
}

// Flag is the message as a backend argument: base64url, so that it passes
// through any shell unquoted.
func (m StartupMessage) Flag() string {
	data, _ := json.Marshal(m)
	return base64.RawURLEncoding.EncodeToString(data)
}

// ParseStartupFlag reads what Flag wrote.
func ParseStartupFlag(s string) (StartupMessage, error) {
	var m StartupMessage
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err == nil {
		err = json.Unmarshal(data, &m)
	}
	return m, err
}

// text is the message when it is on, trimmed; "" when off.
func (m StartupMessage) text() string {
	if !m.Enabled {
		return ""
	}
	return strings.TrimSpace(m.Message)
}

// startupPrompt is what a tab gets as its first prompt when its claude
// starts again: the global message, then the tab's own, a blank line
// between; "" for neither.
func startupPrompt(global StartupMessage, mode ResumeMode, own string) string {
	tab := ""
	if mode == ResumeCustom {
		tab = strings.TrimSpace(own)
	}
	switch g := global.text(); {
	case g == "":
		return tab
	case tab == "":
		return g
	default:
		return g + "\n\n" + tab
	}
}
