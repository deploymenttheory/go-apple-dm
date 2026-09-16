//go:build !windows

package dmctl_test

import (
	"os"
	"testing"
)

func makeWorldReadable(t *testing.T, path string) {
	t.Helper()
	// #nosec G302 -- Deliberately public permissions exercise file permission handling.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}
