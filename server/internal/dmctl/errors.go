package dmctl

import "fmt"

// wrapError adds the dmctl error prefix while preserving the error chain and nil
// success.
func wrapError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("dmctl: %w", err)
}
