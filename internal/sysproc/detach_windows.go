package sysproc

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Detach starts a program apart from the one starting it, so that it
// outlives it.
func Detach(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
	return cmd
}
