//go:build !darwin && !linux

package backend

import "errors"

// procCwd can't read another process's folder here.
func procCwd(int) (string, error) {
	return "", errors.New("a process's folder can't be read on this platform")
}
