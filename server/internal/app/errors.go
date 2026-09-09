package app

import (
	"errors"
	"fmt"
)

var errOperation = errors.New("app operation failed")

func wrapError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("app: %w", err)
}
