package hub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreMigratesV0(t *testing.T) {
	dir := t.TempDir()
	// v0.3: the update check's fields at the top; the environments apart.
	for name, data := range map[string]string{
		"environments.json": `[{"name":"devbox","kind":"ssh","target":"devbox"}]`,
		"hub.json":          `{"checkUpdates":false,"everyDays":3,"skip":"v0.3.1"}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	check := func(s hubState) {
		t.Helper()
		if s.Settings.CheckUpdates || s.Settings.EveryDays != 3 || s.Update.Skip != "v0.3.1" || s.Version != 1 {
			t.Errorf("version %d, settings %+v, update %+v", s.Version, s.Settings, s.Update)
		}
		if len(s.Environments) != 2 || s.Environments[0].Kind != KindLocal || s.Environments[1].Name != "devbox" {
			t.Errorf("environments %+v", s.Environments)
		}
	}
	check(st.get())
	for _, name := range []string{"hub.json.v0.bak", "environments.json.bak"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	again, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	check(again.get())
}

func TestStoreRefusesNewer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hub.json"), []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openStore(dir); err == nil {
		t.Error("opened a file from a newer version")
	}
}

func TestStoreAddsLocal(t *testing.T) {
	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := st.get()
	if envs := s.Environments; len(envs) != 1 || envs[0].Kind != KindLocal {
		t.Errorf("environments %+v", envs)
	}
	if !s.Settings.CheckUpdates || s.Settings.EveryDays != 1 || s.Settings.Resume.Enabled || s.Settings.Resume.Message == "" {
		t.Errorf("settings %+v", s.Settings)
	}
}

func TestStoreKeepsLocalWhereMoved(t *testing.T) {
	dir := t.TempDir()
	data := `{"settings":{"checkUpdates":true,"everyDays":1},"environments":[{"name":"devbox","kind":"ssh","target":"devbox"},{"name":"local","kind":"local"}],"update":{}}`
	if err := os.WriteFile(filepath.Join(dir, "hub.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if envs := st.get().Environments; len(envs) != 2 || envs[1].Kind != KindLocal {
		t.Errorf("environments %+v", envs)
	}
}

func TestStoreWritesBack(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.change(func(s *hubState) { s.Update.Skip = "v9.9.9" })
	again, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.get().Update.Skip != "v9.9.9" {
		t.Errorf("got %+v", again.get().Update)
	}
}
