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

func TestMigrate(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("sessions.json", `[
		{"id":"t1","name":"ops","cwd":"/w","claudeSessionId":"s1","wasRunning":true},
		{"id":"t2","name":"dev","group":"g","cwd":"/w","claudeSessionId":"s2"}
	]`)
	write("favourites.json", `[
		{"claudeSessionId":"s1","name":"ops","onRestore":"restart the watch"},
		{"claudeSessionId":"s9","name":"elsewhere","onRestore":"never opened"}
	]`)

	m := &Manager{stateFile: filepath.Join(dir, "state.json")}
	if err := m.load(dir); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range m.sessions {
		got = append(got, s.ID+"|"+s.Group+"|"+s.StartupMessage)
	}
	if want := []string{"t1||restart the watch", "t2|g|"}; !slices.Equal(got, want) {
		t.Errorf("tabs %v, want %v", got, want)
	}
	for _, name := range []string{"state.json", "sessions.json.migrated", "favourites.json.migrated"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
