package backend

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStartupPrompt(t *testing.T) {
	on := StartupMessage{Enabled: true, Message: " you were restarted "}
	for _, c := range []struct {
		global StartupMessage
		mode   ResumeMode
		own    string
		want   string
	}{
		{StartupMessage{}, ResumeOff, "kept", ""},
		{StartupMessage{}, ResumeCustom, " mine ", "mine"},
		{on, ResumeOff, "kept", "you were restarted"},
		{on, ResumeCustom, "restart the watcher", "you were restarted\n\nrestart the watcher"},
		{StartupMessage{Message: "off, kept"}, ResumeCustom, "mine", "mine"},
		{StartupMessage{Enabled: true, Message: "  "}, ResumeCustom, "mine", "mine"},
	} {
		if got := startupPrompt(c.global, c.mode, c.own); got != c.want {
			t.Errorf("global %+v, mode %q, own %q: got %q, want %q", c.global, c.mode, c.own, got, c.want)
		}
	}
}

func TestStartupFlag(t *testing.T) {
	want := StartupMessage{Enabled: true, Message: "it's 'quoted' -- and $HOME"}
	got, err := ParseStartupFlag(want.Flag())
	if err != nil || got != want {
		t.Errorf("got %+v, %v; want %+v", got, err, want)
	}
}

// loadState writes files into a fresh state directory and loads it, with
// old as the hub's resume default from before v0.7.
func loadState(t *testing.T, files map[string]string, old ...StartupMessage) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var def StartupMessage
	if len(old) > 0 {
		def = old[0]
	}
	m := &Manager{stateFile: filepath.Join(dir, "backend.json")}
	if err := m.load(dir, stateSteps(def)); err != nil {
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
	if a.Resume != ResumeCustom || a.ResumeMessage != "restart the watch" || b.Resume != ResumeOff || c.Resume != ResumeOff {
		t.Errorf("resume %+v %+v %+v", a, b, c)
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

// v0.6: tabs that followed the hub's resume default have resume "".
const stateV1 = `{"version":1,"sessions":[
	{"id":"a","name":"ops","cwd":"/w","order":"V"},
	{"id":"b","name":"slack","cwd":"/w","order":"k","resume":"custom","resumeMessage":"watch slack"},
	{"id":"c","name":"quiet","cwd":"/w","order":"s","resume":"off","resumeMessage":"kept"}],"groups":[]}`

func TestMigrateV1(t *testing.T) {
	m, dir := loadState(t, map[string]string{"backend.json": stateV1}, StartupMessage{Enabled: true, Message: "restarted"})
	a, b, c := m.sessions[0], m.sessions[1], m.sessions[2]
	if a.Resume != ResumeCustom || a.ResumeMessage != "restarted" {
		t.Errorf("a tab on the default gets it as its own: %+v", a)
	}
	if b.ResumeMessage != "watch slack" || c.Resume != ResumeOff || c.ResumeMessage != "kept" {
		t.Errorf("a tab's own choice stays: %+v %+v", b, c)
	}
	if _, err := os.Stat(filepath.Join(dir, "backend.json.v1.bak")); err != nil {
		t.Errorf("no backup: %v", err)
	}
	off, _ := loadState(t, map[string]string{"backend.json": stateV1}, StartupMessage{Message: "restarted"})
	if off.sessions[0].Resume != ResumeOff {
		t.Errorf("with the default off, none: %+v", off.sessions[0])
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
	if err := m.load(dir, stateSteps(StartupMessage{})); err == nil {
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
