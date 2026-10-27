package backend

import (
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
