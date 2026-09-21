//go:build !windows

package recovery

import (
	"os"
	"testing"
)

// makeUnreadable removes Unix file permissions for unreadability tests and restores them during
// cleanup.
func makeUnreadable(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission contract requires an unprivileged process")
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
}
