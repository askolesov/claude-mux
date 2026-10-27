package hub

import (
	"os"
	"path/filepath"
	"testing"
)

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
