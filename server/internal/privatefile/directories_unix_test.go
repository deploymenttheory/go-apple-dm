//go:build !windows

package privatefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestDirectoryCheckUnixPermissions retains owner-only directory checks and
// rejects symlinks even when their targets are private.
func TestDirectoryCheckUnixPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := Mkdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := CheckDirectory(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("private directory requires Unix mode 0700", err)
	}
	makeWorldReadable(t, dir)
	if err := CheckDirectory(dir); !errors.Is(err, os.ErrPermission) {
		t.Fatal("broad directory access accepted", err)
	}
	if err := Mkdir(dir); !errors.Is(err, os.ErrExist) {
		t.Fatal("directory collision was not refused", err)
	}
	if err := CheckDirectory(dir); !errors.Is(err, os.ErrPermission) {
		t.Fatal("collision repaired an existing broad directory", err)
	}
	// #nosec G302 -- Restore owner-only directory traversal after the broad-access negative fixture.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := CheckDirectory(link); !errors.Is(err, os.ErrPermission) {
		t.Fatal("directory symlink accepted", err)
	}
}
