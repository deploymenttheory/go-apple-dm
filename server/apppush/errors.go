package apppush

// wrapError returns the error unchanged. It once prefixed the package name, which
// stacked into chains such as "dmctl: app: app:" that narrated the call path
// instead of the problem; the binary that renders an error names itself, once.
func wrapError(err error) error { return err }
