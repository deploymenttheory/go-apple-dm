//go:build !windows

package privatefile

import (
	"os"
	"testing"
)

func makeWorldReadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}
