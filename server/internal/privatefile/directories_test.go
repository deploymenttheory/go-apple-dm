package privatefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestDirectoryCheckRejectsWrongTypes checks that the directory contract cannot
// be satisfied by a private regular file or an unavailable path.
func TestDirectoryCheckRejectsWrongTypes(t *testing.T) {
	dir := t.TempDir()
	if err := CheckDirectory(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	file, err := OpenFile(filepath.Join(dir, "file"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := CheckDirectory(file.Name()); !errors.Is(err, os.ErrPermission) {
		t.Fatal("regular file accepted as private directory", err)
	}
}
