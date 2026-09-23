package lab

import (
	"errors"
	"fmt"
)

var errOperation = errors.New("lab operation failed")

// wrapError adds the lab error prefix while preserving the error chain and nil
// success.
func wrapError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("lab: %w", err)
}
