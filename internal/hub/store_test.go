package hub

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreMigrates(t *testing.T) {
	dir := t.TempDir()
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
		if s.Settings.CheckUpdates || s.Settings.EveryDays != 3 || s.Update.Skip != "v0.3.1" {
			t.Errorf("settings %+v, update %+v", s.Settings, s.Update)
		}
		if len(s.Environments) != 2 || s.Environments[0].Kind != "local" || s.Environments[1].Name != "devbox" {
			t.Errorf("environments %+v", s.Environments)
		}
	}
	check(st.get())
	if _, err := os.Stat(filepath.Join(dir, "environments.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("environments.json still there: %v", err)
	}
	// And it reads back the same from the new file.
	again, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	check(again.get())
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
	if envs := st.get().Environments; len(envs) != 2 || envs[1].Kind != "local" {
		t.Errorf("environments %+v", envs)
	}
}
