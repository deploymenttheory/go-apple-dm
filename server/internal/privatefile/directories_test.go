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

// TestPrivateDirectoryCreationAndFailures checks exclusive native protection,
// missing-parent failure, and preservation of an existing directory and file.
func TestPrivateDirectoryCreationAndFailures(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "private")
	if err := Mkdir(path); err != nil {
		t.Fatal(err)
	}
	if err := CheckDirectory(path); err != nil {
		t.Fatal("new directory is not private", err)
	}
	file, err := OpenFile(filepath.Join(path, "retained"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("retained fixture"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, existing := range []string{path, file.Name()} {
		if err := Mkdir(existing); !errors.Is(err, os.ErrExist) {
			t.Fatal("exclusive directory creation accepted an existing path", err)
		}
	}
	data, err := os.ReadFile(file.Name())
	if err != nil || string(data) != "retained fixture" {
		t.Fatal("directory collision changed retained content", err)
	}
	if err := Mkdir(filepath.Join(parent, "absent", "child")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("directory creation invented missing parents", err)
	}
}
