package app

import (
	"errors"
	"fmt"
)

var errOperation = errors.New("app operation failed")

// wrapError preserves nil success and wraps failures with the application package prefix.
func wrapError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("app: %w", err)
}
