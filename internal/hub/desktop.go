package hub

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/askolesov/claude-mux/internal/sysproc"
)

// pickFolder shows the system's folder picker on this machine, opened at
// start, and returns the folder chosen; "" when it was cancelled. Only the
// local environment's New tab offers it.
func pickFolder(ctx context.Context, start string) (string, error) {
	home, _ := os.UserHomeDir()
	if start == "~" || strings.HasPrefix(start, "~/") {
		start = filepath.Join(home, start[1:])
	}
	if st, err := os.Stat(start); err != nil || !st.IsDir() {
		start = home
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "osascript",
			"-e", `on run argv`,
			"-e", `POSIX path of (choose folder with prompt "Directory for the new tab" default location (POSIX file (item 1 of argv)))`,
			"-e", `end run`, start)
	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.FolderBrowserDialog; ` +
			`$d.SelectedPath = $env:CMUX_START; if ($d.ShowDialog() -eq 'OK') { $d.SelectedPath }`
		cmd = sysproc.Hide(exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA", "-Command", script))
		cmd.Env = append(os.Environ(), "CMUX_START="+start)
	default:
		if zenity, err := exec.LookPath("zenity"); err == nil {
			cmd = exec.CommandContext(ctx, zenity, "--file-selection", "--directory", "--filename="+start+"/")
		} else if kdialog, err := exec.LookPath("kdialog"); err == nil {
			cmd = exec.CommandContext(ctx, kdialog, "--getexistingdirectory", start)
		} else {
			return "", errors.New("no folder picker here: install zenity or kdialog, or type the path")
		}
	}
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "", nil // cancelled: every picker exits non-zero
	}
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(out))
	if len(path) > 1 {
		path = strings.TrimRight(path, `/\`)
	}
	return path, nil
}

// openLog opens hub.log in the system's viewer. The hub writes it when it
// isn't started from a terminal.
func openLog(stateDir string) error {
	path := filepath.Join(stateDir, "hub.log")
	if _, err := os.Stat(path); err != nil {
		return errors.New("no log: claude-mux started from a terminal writes there instead")
	}
	return sysproc.Open(path)
}
