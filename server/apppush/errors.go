package apppush

import "fmt"

func wrapError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("apppush: %w", err)
}
