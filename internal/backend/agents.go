package backend

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/askolesov/claude-mux/internal/sysproc"
)

// agentKinds are the kinds that run an agent, in the order the page lists them.
var agentKinds = []Kind{KindClaude, KindCodex}

// Agent is what the backend knows about an agent's CLI in the environment.
type Agent struct {
	Path      string `json:"path"`      // "" when there is none
	Version   string `json:"version"`   // as `<agent> --version` prints it
	Installer string `json:"installer"` // the official install command InstallAgent runs here
}

// errNoAgent says a tab can't start: its agent isn't installed here.
func errNoAgent(k Kind) error { return fmt.Errorf("%s isn't installed in this environment", k) }

// agentPlaces are where an agent's official installer puts it, for when it
// isn't on the PATH: a non-interactive SSH command often runs without
// ~/.local/bin on its PATH.
func agentPlaces(k Kind, home string) []string {
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	places := []string{filepath.Join(home, ".local", "bin", string(k)+exe)}
	if k == KindClaude {
		places = append(places, filepath.Join(home, ".claude", "local", "claude"+exe))
	}
	return places
}

// findAgent looks for an agent on the PATH, then where its installer puts it.
func findAgent(k Kind) Agent {
	path, err := exec.LookPath(string(k))
	if err != nil {
		home, _ := os.UserHomeDir()
		for _, p := range agentPlaces(k, home) {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				path = p
				break
			}
		}
	}
	_, installer := installCommand(k)
	if path == "" {
		return Agent{Installer: installer}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := sysproc.Hide(exec.CommandContext(ctx, path, "--version")).Output()
	return Agent{Path: path, Version: strings.TrimSpace(string(out)), Installer: installer}
}

// installCommand is an agent's official installer for this platform: the
// command to run and the line it runs, to show. A terminal has none.
func installCommand(k Kind) (argv []string, line string) {
	windows := runtime.GOOS == "windows"
	switch {
	case k == KindClaude && windows:
		line = "irm https://claude.ai/install.ps1 | iex"
	case k == KindClaude:
		line = "curl -fsSL https://claude.ai/install.sh | bash"
	case k == KindCodex && windows:
		line = "irm https://chatgpt.com/codex/install.ps1 | iex"
	case k == KindCodex:
		line = "curl -fsSL https://chatgpt.com/codex/install.sh | sh"
	default:
		return nil, ""
	}
	if windows {
		return []string{"powershell", "-NoProfile", "-Command", line}, line
	}
	return []string{"bash", "-c", line}, line
}

// InstallAgent runs an agent's official installer, passing its output to out
// as it comes. Then it looks for the agent again and starts the tabs that
// couldn't start without it.
func (m *Manager) InstallAgent(ctx context.Context, k Kind, out io.Writer) error {
	argv, _ := installCommand(k)
	if argv == nil {
		return fmt.Errorf("%s has no installer", k)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := sysproc.Hide(exec.CommandContext(ctx, argv[0], argv[1:]...))
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	m.CheckAgent(k)
	return err
}

// CheckAgent looks for an agent again, for one installed by hand, and starts
// that kind's tabs that couldn't start without it.
func (m *Manager) CheckAgent(k Kind) Agent {
	a := findAgent(k)
	m.mu.Lock()
	m.agents[k] = a
	m.mu.Unlock()
	if a.Path != "" {
		for _, s := range m.List() {
			if s.Kind == k && s.Status == StatusStopped {
				_ = m.Start(s.ID)
			}
		}
	}
	m.changes.Notify()
	return a
}

// findAgents looks for every agent.
func findAgents() map[Kind]Agent {
	agents := map[Kind]Agent{}
	for _, k := range agentKinds {
		agents[k] = findAgent(k)
	}
	return agents
}
