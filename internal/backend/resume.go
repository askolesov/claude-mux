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
	ResumeOff    ResumeMode = "off"    // nothing
)

func (r ResumeMode) valid() error {
	if r != ResumeCustom && r != ResumeOff {
		return fmt.Errorf("unknown resume mode %q", r)
	}
	return nil
}

// ResumeDefault is the message tabs got by default before v0.7, when the
// hub kept one for every tab. The hub still passes it to a backend at
// launch (Flag), only so that a backend.json from before then upgrades with
// each tab keeping the message it had (migrations.go).
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

// resumeMessage is what a tab gets when it starts again.
func resumeMessage(mode ResumeMode, own string) string {
	if mode != ResumeCustom {
		return ""
	}
	return strings.TrimSpace(own)
}
