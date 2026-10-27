// Package lockfile makes a program the only one of its kind for a state
// directory: the hub and the backend each hold one. The lock is the OS's,
// so it goes with the process however it ends.
package lockfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Held is the error when another process holds the lock; Holder is what it
// wrote there.
type Held struct{ Holder string }

func (h *Held) Error() string { return "held by " + h.Holder }

// Acquire locks path and writes holder (who and where) into it. Keep the
// file open for as long as the lock should hold; closing it lets go.
func Acquire(path, holder string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		data, _ := os.ReadFile(path)
		return nil, &Held{Holder: strings.TrimSpace(string(data))}
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(holder+"\n"), 0)
	return f, nil
}

// IsHeld says whether err is another process holding the lock, and gives
// what it wrote.
func IsHeld(err error) (string, bool) {
	var h *Held
	if errors.As(err, &h) {
		return h.Holder, true
	}
	return "", false
}
