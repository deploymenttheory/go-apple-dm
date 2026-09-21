package recovery

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/deploymenttheory/go-apple-dm/server/internal/privatefile"
)

// openLocal opens a recovery file relative to its parent directory root through the
// private-file helper.
func openLocal(path string, flags int, mode os.FileMode) (*os.File, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, wrap(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(root.Close)
	f, err := privatefile.OpenRootFile(root, filepath.Base(path), flags, mode)
	return f, wrap(err)
}

// Config and key files are bounded independently of the streamed SQL snapshot.
const maxConfigFile = 4 << 20

// readPrivate reads a regular local file within the configuration-size limit.
func readPrivate(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, wrap(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(root.Close)
	name := filepath.Base(path)
	info, err := root.Lstat(name)
	if err != nil {
		return nil, wrap(err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxConfigFile {
		return nil, ErrInvalid
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, wrap(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(f.Close)
	raw, err := io.ReadAll(io.LimitReader(f, maxConfigFile+1))
	if err != nil {
		return nil, wrap(err)
	}
	if len(raw) > maxConfigFile {
		return nil, ErrLimit
	}
	return raw, nil
}

// writePrivate writes local recovery material with the private-file protections required by
// the caller.
func writePrivate(path string, raw []byte) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return wrap(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(root.Close)
	f, err := privatefile.OpenRootFile(root, filepath.Base(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return wrap(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(f.Close)
	if _, err := f.Write(raw); err != nil {
		return wrap(err)
	}
	return wrap(f.Sync())
}

// copyRegular copies a regular recovery file without accepting unsupported file types.
func copyRegular(ctx context.Context, source, destination string) error {
	if err := ctx.Err(); err != nil {
		return wrap(err)
	}
	raw, err := readPrivate(source)
	if err != nil {
		return err
	}
	return writePrivate(destination, raw)
}
