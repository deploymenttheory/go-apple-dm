package bench

import (
	"errors"
	"fmt"
)

var errOperation = errors.New("bench operation failed")

// wrapError adds the bench error prefix while preserving the error chain and nil
// success.
func wrapError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("bench: %w", err)
}
