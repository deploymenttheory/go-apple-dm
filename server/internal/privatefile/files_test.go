package privatefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateCreationAndAccess(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	for _, tc := range []struct {
		name   string
		create func() (*os.File, error)
	}{
		{"temporary", func() (*os.File, error) { return CreateTemp(dir, "secret-*") }},
		{"exclusive", func() (*os.File, error) {
			return OpenFile(filepath.Join(dir, "exclusive"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		}},
		{"root", func() (*os.File, error) { return OpenRootFile(root, "root", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := tc.create()
			if err != nil {
				t.Fatal(err)
			}
			path := file.Name()
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if err := Check(path); err != nil {
				t.Fatalf("new file is not private: %v", err)
			}
			makeWorldReadable(t, path)
			if err := Check(path); !errors.Is(err, os.ErrPermission) {
				t.Fatalf("public access accepted: %v", err)
			}
			if err := Protect(path); err != nil {
				t.Fatal(err)
			}
			if err := Check(path); err != nil {
				t.Fatal(err)
			}
		})
	}
	read, err := OpenRootFile(root, "root", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = read.Close()
	if _, err := OpenRootFile(root, "../outside", os.O_RDONLY, 0); err == nil {
		t.Fatal("root escape accepted")
	}
	if _, err := OpenFile(filepath.Join(dir, "exclusive"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
}

func TestPrivateFileFailures(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing", "file")
	if _, err := CreateTemp(missing, "secret-*"); err == nil {
		t.Fatal("missing parent accepted")
	}
	if err := Check(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := Check(dir); !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	if err := Protect(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(dir, "removed-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file.Name()); err != nil {
		t.Fatal(err)
	}
	if _, err := prepare(file, nil); err == nil {
		t.Fatal("failed protection accepted")
	}
	if err := SyncDirectory(file); err == nil {
		t.Fatal("closed handle accepted")
	}
	// #nosec G304 -- The test controls this fixture path within its private workspace.
	directory, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(directory.Close)
	if err := SyncDirectory(directory); err != nil {
		t.Fatal(err)
	}
}

func TestProtectionFollowsOpenedFile(t *testing.T) {
	dir := t.TempDir()
	original, moved := filepath.Join(dir, "original"), filepath.Join(dir, "moved")
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(root.Close)
	// Root.OpenFile permits renaming an open file on Windows. os.OpenFile
	// omits FILE_SHARE_DELETE, which would prevent this test's path replacement.
	file, err := root.OpenFile("original", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(file.Close)
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	// #nosec G306 -- Deliberately public permissions exercise file permission handling.
	if err := os.WriteFile(original, []byte("unrelated replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeWorldReadable(t, original)
	makeWorldReadable(t, moved)
	if _, err := prepare(file, nil); err != nil {
		t.Fatal(err)
	}
	if err := Check(moved); err != nil {
		t.Fatalf("opened file was not protected: %v", err)
	}
	if err := Check(original); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("replacement file permissions changed: %v", err)
	}
}
