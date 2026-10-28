package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveFile(t *testing.T) {
	m := tree(t)
	first, err := m.SaveFile("t1", "../../shot.png", []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(first) != m.filesDir("t1") || filepath.Base(first) != "shot.png" {
		t.Errorf("saved at %s", first)
	}
	second, err := m.SaveFile("t1", "shot.png", []byte("b"))
	if err != nil || filepath.Base(second) != "shot 2.png" {
		t.Errorf("second: %s, %v", second, err)
	}
	if data, _ := os.ReadFile(first); string(data) != "a" {
		t.Errorf("the first was overwritten: %q", data)
	}
	if _, err := m.SaveFile("nope", "x", nil); err == nil {
		t.Error("saved for a missing tab")
	}
	if _, err := m.SaveFile("t1", "..", nil); err == nil {
		t.Error("saved a file without a name")
	}
	if err := m.Close("t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.filesDir("t1")); !os.IsNotExist(err) {
		t.Errorf("a closed tab's files stay: %v", err)
	}
}
