package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/askolesov/claude-mux/internal/lockfile"
)

// lockState makes this the only backend of the environment: two would both
// resume every tab, forking its conversation, and overwrite each other's
// backend.json. The hub that lost retries until it's free.
func lockState(stateDir string) (*os.File, error) {
	f, err := lockfile.Acquire(filepath.Join(stateDir, "backend.lock"), strconv.Itoa(os.Getpid()))
	if pid, held := lockfile.IsHeld(err); held {
		return nil, fmt.Errorf("this environment is in use by another claude-mux (pid %s); it connects once that one is gone", pid)
	}
	return f, err
}
