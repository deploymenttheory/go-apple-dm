//go:build !windows

package lab

import (
	"os"
	"testing"
)

// assertAppsBooksPrivateFile checks the exact Unix evidence-file contract.
func assertAppsBooksPrivateFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatal("evidence file requires owner-only read/write access", path, info.Mode())
	}
}

// assertAppsBooksPrivateDirectory checks the exact Unix evidence-directory contract.
func assertAppsBooksPrivateDirectory(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatal("evidence directory requires owner-only access", path, info.Mode())
	}
}
