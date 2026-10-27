//go:build !windows

package sysproc

import (
	"os/exec"
	"syscall"
)

// Detach starts a program in a session of its own, so that it outlives the
// one starting it.
func Detach(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
