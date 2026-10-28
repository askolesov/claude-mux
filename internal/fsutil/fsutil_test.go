package fsutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("checksum")
	err := WriteFile(path, 0o600, func(w io.Writer) error {
		_, _ = io.WriteString(w, "half")
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("got %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "old" {
		t.Errorf("a failed write changed the file: %q", data)
	}
	if err := WriteFile(path, 0o755, func(w io.Writer) error { _, err := io.WriteString(w, "new"); return err }); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "new" {
		t.Errorf("got %q", data)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", st.Mode())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("left behind: %v", entries)
	}
	if err := CopyFile(path, filepath.Join(dir, "y"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "y")); string(data) != "new" {
		t.Errorf("copy %q", data)
	}
}
