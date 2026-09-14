// Package privatefile creates and checks files containing local credentials.
// Unix uses owner-only modes; Windows uses an explicit access control list.
package privatefile

import (
	"fmt"
	"os"
)

// CreateTemp creates an empty private file before the caller writes any data.
func CreateTemp(dir, pattern string) (*os.File, error) {
	f, err := os.CreateTemp(dir, pattern)
	return prepare(f, err)
}

// OpenFile creates a private file. Callers must request exclusive creation.
func OpenFile(path string, flags int, mode os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, flags, mode) // #nosec G304 -- explicit local operator path
	return prepare(f, err)
}

// OpenRootFile preserves the caller's os.Root traversal boundary.
func OpenRootFile(root *os.Root, name string, flags int, mode os.FileMode) (*os.File, error) {
	f, err := root.OpenFile(name, flags, mode)
	if err != nil || flags&os.O_CREATE == 0 {
		return f, wrap(err)
	}
	return prepare(f, nil)
}

func prepare(file *os.File, err error) (*os.File, error) {
	if err != nil {
		return nil, wrap(err)
	}
	if err := protectFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func wrap(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("private file: %w", err)
}
