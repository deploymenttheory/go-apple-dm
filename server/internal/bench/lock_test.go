package bench

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceLockExcludesOtherHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bench.lock")
	open := func() *os.File {
		t.Helper()
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
