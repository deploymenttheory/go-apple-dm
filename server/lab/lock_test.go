package lab

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWorkspaceLockExcludesOtherHandles checks that workspace lock excludes other handles.
func TestWorkspaceLockExcludesOtherHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lab.lock")
	open := func() *os.File {
		t.Helper()
		// #nosec G304 -- The test controls this fixture path within its private workspace.
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		return file
	}
	first, second := open(), open()
	if err := lockWorkspace(first); err != nil {
		t.Fatal(err)
	}
	if err := lockWorkspace(second); err == nil {
		t.Fatal("another workspace supervisor acquired the held lock")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lockWorkspace(second); err != nil {
		t.Fatalf("closed supervisor retained the lock: %v", err)
	}
}
