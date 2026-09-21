package runtime

import "fmt"

// wrapError adds the runtime package context to nonnil errors.
func wrapError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("runtime: %w", err)
}
