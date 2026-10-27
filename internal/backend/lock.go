package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// lockState makes this the only backend of the environment: two would both
// resume every tab, forking its conversation, and overwrite each other's
// state.json. The lock is the OS's, so it goes with the process however it
// ends, and the hub that lost retries until it's free. Keep the file open
// for as long as the backend runs.
func lockState(stateDir string) (*os.File, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(stateDir, "backend.lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		holder := "another claude-mux"
		if pid, _ := os.ReadFile(path); len(pid) > 0 {
			holder += " (pid " + strings.TrimSpace(string(pid)) + ")"
		}
		return nil, fmt.Errorf("this environment is in use by %s; it connects once that one is gone", holder)
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return f, nil
}
