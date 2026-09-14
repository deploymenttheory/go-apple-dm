package bench

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	cmd.Cancel = func() error {
		pid, err := strconv.ParseUint(strconv.Itoa(cmd.Process.Pid), 10, 32)
		if err != nil {
			return wrapError(err)
		}
		if pid == 0 {
			return wrapError(os.ErrInvalid)
		}
		// Go's os.Interrupt handler receives CTRL_BREAK, allowing ordered shutdown.
		// Console-less callers cannot deliver console events; terminate that child.
		if err := windows.GenerateConsoleCtrlEvent(
			windows.CTRL_BREAK_EVENT, uint32(pid),
		); err != nil {
			return wrapError(cmd.Process.Kill())
		}
		return nil
	}
}
