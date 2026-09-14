package bench

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	cmd.Cancel = func() error {
		// Go's os.Interrupt handler receives CTRL_BREAK, allowing ordered shutdown.
		// Console-less callers cannot deliver console events; terminate that child.
		if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid)); err != nil {
			return wrapError(cmd.Process.Kill())
		}
		return nil
	}
}
