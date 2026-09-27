package filesystem

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/internal/blobutil"
)

// Store confines package objects to an already-created private directory.
// The directory must reside on a filesystem supporting atomic hard links.
type Store struct {
	root   *os.Root
	create func(string) (stagedFile, error)
	open   func(string) (storedFile, error)
}

// stagedFile represents the durability operations required before publication.
type stagedFile interface {
	io.Writer
	io.Closer
	Sync() error
}

// storedFile supports bounded reads and validation of regular-file metadata.
type storedFile interface {
	io.ReaderAt
	io.Closer
	Stat() (os.FileInfo, error)
}

var _ applications.BlobStore = (*Store)(nil)

// New opens a traversal-resistant filesystem root. Close it after all users stop.
func New(directory string) (*Store, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &Store{
		root: root,
		create: func(name string) (stagedFile, error) {
			return root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		},
		open: func(name string) (storedFile, error) { return root.Open(name) },
	}, nil
}

// Close releases the root directory handle.
func (s *Store) Close() error { return s.root.Close() }

// Put stages a private file, syncs it and atomically publishes it without replacing
// existing content. A failed copy never becomes visible under the destination key.
func (s *Store) Put(ctx context.Context, key string, r io.ReadSeeker, size int64) error {
	if err := blobutil.Key(key); err != nil {
		return err
	}
	if err := blobutil.Input(ctx, r, size); err != nil {
		return err
	}
	if err := s.root.MkdirAll(path.Dir(key), 0o700); err != nil {
		return err
	}
	// Create a random staging file relative to the already-opened root.
	temporary := path.Join(path.Dir(key), ".upload-"+rand.Text())
	file, err := s.create(temporary)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = s.root.Remove(temporary) }()
	n, err := io.Copy(file, blobutil.Reader{Context: ctx, Input: r})
	if err != nil {
		return err
	}
	if n != size {
		return applications.ErrIntegrity
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = s.root.Link(temporary, key); errors.Is(err, os.ErrExist) {
		return applications.ErrConflict
	}
	return err
}

// Open returns a bounded section of a private object. The caller closes it.
func (s *Store) Open(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if err := blobutil.Key(key); err != nil {
		return nil, err
	}
	if err := blobutil.Range(offset, length); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := s.open(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil, applications.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || offset > info.Size() || length > info.Size()-offset {
		_ = f.Close()
		return nil, applications.ErrInvalid
	}
	if length < 0 {
		length = info.Size() - offset
	}
	return &section{Reader: blobutil.Reader{Context: ctx, Input: io.NewSectionReader(f, offset, length)}, file: f}, nil
}

type section struct {
	io.Reader
	file io.Closer
}

// Close releases the file owned by the bounded reader.
func (s *section) Close() error { return s.file.Close() }

// Delete succeeds for an absent object and never follows paths outside the root.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := blobutil.Key(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := s.root.Remove(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Import opens a local source under the same traversal-resistant root. The source
// is streamed through Manager.Upload, so it receives every normal package check.
func (s *Store) Import(ctx context.Context, m *applications.Manager, id, revision, backend, key string) (applications.Record, error) {
	if err := blobutil.Key(key); err != nil {
		return applications.Record{}, err
	}
	r, err := s.Open(ctx, key, 0, -1)
	if err != nil {
		return applications.Record{}, err
	}
	defer func() { _ = r.Close() }()
	return m.Upload(ctx, id, revision, backend, applications.Source{Kind: "file", Location: strings.TrimPrefix(key, "./")}, r, "")
}
