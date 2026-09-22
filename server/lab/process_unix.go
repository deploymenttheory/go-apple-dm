//go:build !windows

package lab

import (
	"os/exec"
	"syscall"
)

// configureChild makes cancellation signal the child with SIGTERM.
func configureChild(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return wrapError(cmd.Process.Signal(syscall.SIGTERM)) }
}
