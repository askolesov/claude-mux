package backend

import (
	"os"
	"strconv"
)

// procCwd is a process's current folder.
func procCwd(pid int) (string, error) {
	return os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
}
