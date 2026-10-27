package backend

import (
	"math/rand/v2"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestKeyBetween(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	keys := []string{}
	for range 2000 {
		at := r.IntN(len(keys) + 1)
		lo, hi := "", ""
		if at > 0 {
			lo = keys[at-1]
		}
		if at < len(keys) {
			hi = keys[at]
		}
		k := keyBetween(lo, hi)
		if (lo != "" && k <= lo) || (hi != "" && k >= hi) || strings.HasSuffix(k, "0") {
			t.Fatalf("keyBetween(%q, %q) = %q", lo, hi, k)
		}
		keys = slices.Insert(keys, at, k)
	}
	if !slices.IsSorted(keys) {
		t.Error("keys out of order")
	}
}

// tree is a manager with tabs and groups, saving into a temp dir.
func tree(t *testing.T) *Manager {
	m := &Manager{stateFile: filepath.Join(t.TempDir(), "backend.json")}
	m.groups = []*Group{{ID: "g1", Name: "api", Order: "V"}, {ID: "g2", Name: "pay", Parent: "g1", Order: "V"}}
	m.sessions = []*Session{{ID: "t1", Order: "F"}, {ID: "t2", Parent: "g1", Order: "F"}, {ID: "t3", Parent: "g2", Order: "V"}}
	return m
}

// children lists parent's children ids in order.
func children(m *Manager, parent string) []string {
	var ids []string
	for _, n := range m.siblings(parent, "") {
		ids = append(ids, n.id)
	}
	return ids
}

func TestMove(t *testing.T) {
	m := tree(t)
	if got := children(m, "g1"); !slices.Equal(got, []string{"t2", "g2"}) {
		t.Fatalf("g1 %v", got)
	}
	if err := m.Move("g1", "g2", "", ""); err == nil {
		t.Error("moved a group into its own child")
	}
	if err := m.Move("g1", "g1", "", ""); err == nil {
		t.Error("moved a group into itself")
	}
	if err := m.Move("t1", "g1", "g2", ""); err != nil {
		t.Fatal(err)
	}
	if got := children(m, "g1"); !slices.Equal(got, []string{"t2", "t1", "g2"}) {
		t.Errorf("g1 %v", got)
	}
	if err := m.Move("g2", "", "", "g1"); err != nil {
		t.Fatal(err)
	}
	if got := children(m, ""); !slices.Equal(got, []string{"g1", "g2"}) {
		t.Errorf("root %v", got)
	}
	if got := children(m, "g2"); !slices.Equal(got, []string{"t3"}) {
		t.Errorf("g2 took its tab along: %v", got)
	}
}

func TestDeleteGroup(t *testing.T) {
	m := tree(t)
	m.sessions = append(m.sessions, &Session{ID: "t4", Order: "k"}) // after g1 at the root
	if err := m.DeleteGroup("g1"); err != nil {
		t.Fatal(err)
	}
	if got := children(m, ""); !slices.Equal(got, []string{"t1", "t2", "g2", "t4"}) {
		t.Errorf("root %v", got)
	}
	if len(m.sessions) != 4 {
		t.Error("a tab was closed")
	}
}

func TestCreateGroup(t *testing.T) {
	m := tree(t)
	g, err := m.CreateGroup(" new ", "g1", "t2")
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "new" || !slices.Equal(children(m, "g1"), []string{"t2", g.ID, "g2"}) {
		t.Errorf("%+v, g1 %v", g, children(m, "g1"))
	}
	if _, err := m.CreateGroup("x", "nope", ""); err == nil {
		t.Error("created a group in a missing parent")
	}
}
