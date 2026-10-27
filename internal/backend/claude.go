package backend

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var errNoClaude = errors.New("claude isn't installed in this environment")

// Claude is what the backend knows about the environment's claude.
type Claude struct {
	Path      string `json:"path"`      // "" when there is none
	Version   string `json:"version"`   // as `claude --version` prints it
	Installer string `json:"installer"` // the official install command InstallClaude runs here
}

// findClaude looks for claude on the PATH, then where the official installer
// puts it: a non-interactive SSH command often runs without ~/.local/bin on
// its PATH.
func findClaude() Claude {
	path, err := exec.LookPath("claude")
	if err != nil {
		home, _ := os.UserHomeDir()
		for _, p := range []string{filepath.Join(home, ".local", "bin", "claude"), filepath.Join(home, ".claude", "local", "claude")} {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				path = p
				break
			}
		}
	}
	_, installer := installCommand()
	if path == "" {
		return Claude{Installer: installer}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, path, "--version").Output()
	return Claude{Path: path, Version: strings.TrimSpace(string(out)), Installer: installer}
}

// installCommand is Anthropic's official installer for this platform: the
// command to run and the line it runs, to show.
func installCommand() (argv []string, line string) {
	if runtime.GOOS == "windows" {
		line = "irm https://claude.ai/install.ps1 | iex"
		return []string{"powershell", "-NoProfile", "-Command", line}, line
	}
	line = "curl -fsSL https://claude.ai/install.sh | bash"
	return []string{"bash", "-c", line}, line
}

// InstallClaude runs the official installer and returns its output. Then it
// looks for claude again and starts the tabs that couldn't start without it.
func (m *Manager) InstallClaude(ctx context.Context) (string, error) {
	argv, _ := installCommand()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	m.CheckClaude()
	return string(out), err
}

// CheckClaude looks for claude again, for one installed by hand, and starts
// the tabs that couldn't start without it.
func (m *Manager) CheckClaude() Claude {
	c := findClaude()
	m.mu.Lock()
	m.claude = c
	m.mu.Unlock()
	if c.Path != "" {
		for _, s := range m.List() {
			if s.Status == StatusStopped {
				_ = m.Start(s.ID)
			}
		}
	}
	m.notify()
	return c
}

func (m *Manager) claudePath() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.claude.Path
}
