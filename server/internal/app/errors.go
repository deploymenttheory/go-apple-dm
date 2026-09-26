package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

var errOperation = errors.New("app operation failed")

// wrapError returns the error unchanged. It once prefixed the package name, which
// stacked into chains such as "dmctl: app: app:" that narrated the call path
// instead of the problem; the binary that renders an error names itself, once.
func wrapError(err error) error { return err }

// configf reports a configuration the operator must change, as one sentence: the
// setting, the value and what would be accepted, with the operand once and no layer
// labels. It matches ErrConfig for callers and unwraps to cause when there is one.
func configf(cause error, format string, args ...any) error {
	return &configError{msg: fmt.Sprintf(format, args...), cause: cause}
}

// configError is the sentence configf writes.
type configError struct {
	msg   string
	cause error
}

// Error returns the sentence.
func (e *configError) Error() string { return e.msg }

// Unwrap returns the cause, so errors.Is against fs.ErrNotExist and the like still hold.
func (e *configError) Unwrap() error { return e.cause }

// Is matches ErrConfig.
func (e *configError) Is(target error) bool { return target == ErrConfig }

// reason returns the operating system's own words for a file or network failure,
// without the operation and path Go's wrappers repeat: "no such file or directory",
// "permission denied".
func reason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Err != nil {
		err = pe.Err
	}
	var se *os.SyscallError
	if errors.As(err, &se) && se.Err != nil {
		err = se.Err
	}
	return err.Error()
}
