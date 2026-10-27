package backend

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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

func TestResumeFlag(t *testing.T) {
	want := ResumeDefault{Enabled: true, Message: "it's 'quoted' -- and $HOME"}
	got, err := ParseResumeFlag(want.Flag())
	if err != nil || got != want {
		t.Errorf("got %+v, %v; want %+v", got, err, want)
	}
}

// loadState writes files into a fresh state directory and loads it.
func loadState(t *testing.T, files map[string]string) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := &Manager{stateFile: filepath.Join(dir, "backend.json")}
	if err := m.load(dir); err != nil {
		t.Fatal(err)
	}
	return m, dir
}

// v0.4–v0.6: backend.json without a version, groups as names, and before
// v0.5 a startupMessage.
const stateV0 = `{"sessions":[
	{"id":"a","name":"ops","cwd":"/w","group":"watch","startupMessage":"restart the watch"},
	{"id":"b","name":"notes","cwd":"/w"},
	{"id":"c","name":"slack","cwd":"/w","group":"watch","resume":"off"}]}`

func TestMigrateV0(t *testing.T) {
	m, dir := loadState(t, map[string]string{"backend.json": stateV0})
	if len(m.groups) != 1 || m.groups[0].Name != "watch" || m.groups[0].Parent != "" {
		t.Fatalf("groups %+v", m.groups)
	}
	g := m.groups[0].ID
	a, b, c := m.sessions[0], m.sessions[1], m.sessions[2]
	if a.Parent != g || c.Parent != g || b.Parent != "" {
		t.Errorf("parents %q %q %q, group %q", a.Parent, b.Parent, c.Parent, g)
	}
	// The group sits where its first tab was: before notes.
	if m.groups[0].Order >= b.Order || a.Order >= c.Order {
		t.Errorf("orders: group %q, notes %q; ops %q, slack %q", m.groups[0].Order, b.Order, a.Order, c.Order)
	}
	if a.Resume != ResumeCustom || a.ResumeMessage != "restart the watch" || c.Resume != ResumeOff {
		t.Errorf("resume %+v %+v", a, c)
	}
	if _, err := os.Stat(filepath.Join(dir, "backend.json.v0.bak")); err != nil {
		t.Errorf("no backup: %v", err)
	}
	// Written back at the current version, and it reads back the same.
	again, _ := loadState(t, map[string]string{"backend.json": mustRead(t, filepath.Join(dir, "backend.json"))})
	if len(again.groups) != 1 || again.sessions[0].Parent != again.groups[0].ID {
		t.Errorf("reloaded %+v", again.groups)
	}
}

func TestMigrateStateJSON(t *testing.T) {
	m, dir := loadState(t, map[string]string{"state.json": stateV0, "hooks.json": `{}`})
	if len(m.sessions) != 3 {
		t.Fatalf("sessions %+v", m.sessions)
	}
	if _, err := os.Stat(filepath.Join(dir, "backend.json")); err != nil {
		t.Error(err)
	}
	for _, name := range []string{"state.json", "hooks.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still there: %v", name, err)
		}
	}
}

func TestRefuseNewer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "backend.json"), []byte(`{"version":99,"sessions":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{stateFile: filepath.Join(dir, "backend.json")}
	if err := m.load(dir); err == nil {
		t.Error("loaded a file from a newer version")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
