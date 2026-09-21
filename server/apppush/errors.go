package apppush

import "fmt"

// wrapError adds the application-push package context to nonnil errors.
func wrapError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("apppush: %w", err)
}
