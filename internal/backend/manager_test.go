package backend

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGroupTogether(t *testing.T) {
	tabs := func(spec ...string) []*Session { // "id:group"
		var ss []*Session
		for _, s := range spec {
			id, group, _ := strings.Cut(s, ":")
			ss = append(ss, &Session{ID: id, Group: group})
		}
		return ss
	}
	ids := func(ss []*Session) []string {
		var out []string
		for _, s := range ss {
			out = append(out, s.ID)
		}
		return out
	}
	for _, tc := range []struct {
		name string
		in   []*Session
		want []string
	}{
		{"no groups keep their order", tabs("a:", "b:", "c:"), []string{"a", "b", "c"}},
		{"a group gathers at its first tab", tabs("a:x", "b:", "c:x", "d:"), []string{"a", "c", "b", "d"}},
		{"two groups interleaved", tabs("a:x", "b:y", "c:x", "d:y"), []string{"a", "c", "b", "d"}},
		{"ungrouped before a group stays first", tabs("a:", "b:x", "c:", "d:x"), []string{"a", "b", "d", "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manager{sessions: tc.in}
			m.groupTogether()
			if got := ids(m.sessions); !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLoadStartupMessage(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "backend.json")
	data := `{"sessions":[{"id":"t1","name":"ops","cwd":"/w","startupMessage":"restart the watch"}]}`
	if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{stateFile: file}
	if err := m.load(); err != nil {
		t.Fatal(err)
	}
	if len(m.sessions) != 1 || m.sessions[0].Resume != ResumeCustom || m.sessions[0].ResumeMessage != "restart the watch" || m.sessions[0].StartupMessage != "" {
		t.Fatalf("sessions %+v", m.sessions)
	}
}

func TestResumeFlag(t *testing.T) {
	want := ResumeDefault{Enabled: true, Message: "it's 'quoted' -- and $HOME"}
	got, err := ParseResumeFlag(want.Flag())
	if err != nil || got != want {
		t.Errorf("got %+v, %v; want %+v", got, err, want)
	}
}

func TestResumeMessage(t *testing.T) {
	m := &Manager{}
	for _, c := range []struct {
		enabled bool
		mode    ResumeMode
		own     string
		want    string
	}{
		{false, "", "", ""},
		{true, "", "", "global"},
		{true, ResumeOff, "", ""},
		{false, ResumeCustom, " mine ", "mine"},
		{true, ResumeCustom, "mine", "mine"},
	} {
		m.SetResumeDefault(ResumeDefault{Enabled: c.enabled, Message: "global"})
		if got := m.resumeMessage(c.mode, c.own); got != c.want {
			t.Errorf("enabled %v, mode %q, own %q: got %q, want %q", c.enabled, c.mode, c.own, got, c.want)
		}
	}
}
