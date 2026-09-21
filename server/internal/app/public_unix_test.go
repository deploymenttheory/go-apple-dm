//go:build !windows

package app

import (
	"os"
	"testing"
)

// makeWorldReadable sets world-readable Unix permissions on a fixture file.
func makeWorldReadable(t *testing.T, path string) {
	t.Helper()
	// #nosec G302 -- Deliberately public permissions exercise file permission handling.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}
