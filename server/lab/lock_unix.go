//go:build !windows

package lab

import (
	"os"
	"syscall"
)

// Closing the file releases the lock, including after a process exits.
func lockWorkspace(file *os.File) error {
	return wrapError(syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
}
