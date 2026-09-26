package dmctl

import "fmt"

// wrapError returns the error unchanged. It once prefixed the package name, which
// stacked into chains such as "dmctl: app: app:" that narrated the call path
// instead of the problem; the binary that renders an error names itself, once.
func wrapError(err error) error { return err }

// usagef reports a command-line mistake as one sentence that says what was given and
// what the command takes. It matches ErrUsage for the exit code.
func usagef(format string, args ...any) error { return &usageError{msg: fmt.Sprintf(format, args...)} }

// usageError is the sentence usagef writes.
type usageError struct{ msg string }

// Error returns the sentence.
func (e *usageError) Error() string { return e.msg }

// Is matches ErrUsage.
func (e *usageError) Is(target error) bool { return target == ErrUsage }
