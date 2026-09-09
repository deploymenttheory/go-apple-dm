package dmctl

import "fmt"

func wrapError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("dmctl: %w", err)
}
