//go:build !windows

package sysproc

import "os/exec"

// Hide: only Windows gives a helper a window of its own.
func Hide(cmd *exec.Cmd) *exec.Cmd { return cmd }
