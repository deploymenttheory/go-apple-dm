package privatefile

import (
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

// prepare protects an opened file before use, closing it if permissions cannot be secured
// and propagating any opening error.
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

// wrap returns the error unchanged; a *fs.PathError already names the operation and
// the file, and a package label in front of it only lengthens the line.
func wrap(err error) error { return err }
