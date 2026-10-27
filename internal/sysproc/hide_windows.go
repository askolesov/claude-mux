package sysproc

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Hide starts a console program without a console window of its own. The
// Windows hub has no console, so each helper (the backend, ssh, docker,
// claude --version) would otherwise flash one up.
func Hide(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd
}
