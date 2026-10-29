package backend

import (
	"os"
	"os/exec"
	"runtime"
)

// shellOverride, when set, is the shell terminal tabs run, for tests.
var shellOverride string

// shellCommand is the shell a terminal tab runs: the user's login shell,
// PowerShell on Windows.
func shellCommand() (path string, args []string) {
	if shellOverride != "" {
		return shellOverride, nil
	}
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("pwsh"); err == nil {
			return p, []string{"-NoLogo"}
		}
		return "powershell", []string{"-NoLogo"}
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh, []string{"-l"}
	}
	return "/bin/sh", []string{"-l"}
}
