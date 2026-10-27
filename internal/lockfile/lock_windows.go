package lockfile

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile locks a byte far past what the file holds, so that the loser
// can still read who holds it.
func lockFile(f *os.File) error {
	ol := &windows.Overlapped{Offset: 0, OffsetHigh: 1}
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
}
