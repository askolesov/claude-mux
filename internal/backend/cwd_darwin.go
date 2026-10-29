package backend

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// procCwd is a process's current folder, as lsof reports it.
func procCwd(pid int) (string, error) {
	out, err := exec.Command("lsof", "-a", "-d", "cwd", "-p", strconv.Itoa(pid), "-Fn").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return line[1:], nil
		}
	}
	return "", errors.New("lsof named no folder")
}
