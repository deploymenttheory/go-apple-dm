package lab

import (
	"os"

	"golang.org/x/sys/windows"
)

// Lock one byte without waiting. Closing the handle releases the lock.
func lockWorkspace(file *os.File) error {
	return wrapError(windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &windows.Overlapped{},
	))
}
