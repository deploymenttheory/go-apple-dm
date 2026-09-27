package filesystem

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

var errDisk = errors.New("injected disk failure")

// faultFile retains real descriptor ownership while injecting a durability failure.
type faultFile struct {
	stagedFile
	operation string
}

// Write simulates a failed disk write before publication.
func (f *faultFile) Write(b []byte) (int, error) {
	if f.operation == "write" {
		return 0, errDisk
	}
	return f.stagedFile.Write(b)
}

// Sync simulates a durability failure even when buffered writes succeeded.
func (f *faultFile) Sync() error {
	if f.operation == "sync" {
		return errDisk
	}
	return f.stagedFile.Sync()
}

// Close releases the real file and can also report a delayed write failure.
func (f *faultFile) Close() error {
	err := f.stagedFile.Close()
	if f.operation == "close" {
		return errors.Join(err, errDisk)
	}
	return err
}

// faultStat models a descriptor whose metadata becomes unavailable.
type faultStat struct {
	storedFile
	closeCalls int
	closeErr   error
}

// Stat returns a failure that must close the opened descriptor.
func (f *faultStat) Stat() (os.FileInfo, error) { return nil, errDisk }

// Close records the result of releasing the real descriptor, without depending on
// platform-specific errors from calling Stat on an already-closed file.
func (f *faultStat) Close() error {
	f.closeCalls++
	f.closeErr = f.storedFile.Close()
	return f.closeErr
}

// shortSource reports a stale length, as a concurrently truncated source might.
type shortSource struct{ *bytes.Reader }

// Seek supplies the original length when the adapter validates source size.
func (s shortSource) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekEnd {
		return 10, nil
	}
	return s.Reader.Seek(offset, whence)
}

// cancelSource cancels its context after the last source bytes are consumed.
type cancelSource struct {
	*bytes.Reader
	cancel context.CancelFunc
}

// Read triggers cancellation after input validation, before final publication.
func (s cancelSource) Read(p []byte) (int, error) {
	n, err := s.Reader.Read(p)
	s.cancel()
	return n, err
}

// TestDurabilityFailuresNeverPublish checks create, write, sync and close errors
// against a real private root. Failed writes must leave neither target nor staging files.
func TestDurabilityFailuresNeverPublish(t *testing.T) {
	for _, operation := range []string{"create", "write", "sync", "close", "size", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			s, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if e := s.Close(); e != nil {
					t.Error(e)
				}
			}()
			create := s.create
			s.create = func(name string) (stagedFile, error) {
				if operation == "create" {
					return nil, errDisk
				}
				f, e := create(name)
				if e != nil {
					return nil, e
				}
				return &faultFile{stagedFile: f, operation: operation}, nil
			}
			var input io.ReadSeeker = bytes.NewReader([]byte("x"))
			size := int64(1)
			ctx := t.Context()
			if operation == "size" {
				input = shortSource{bytes.NewReader([]byte("x"))}
				size = 10
			}
			if operation == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				input = cancelSource{Reader: bytes.NewReader([]byte("x")), cancel: cancel}
			}
			if err = s.Put(ctx, "package.pkg", input, size); err == nil {
				t.Fatal("failed operation published a package")
			}
			entries, e := os.ReadDir(root)
			if e != nil {
				t.Fatal(e)
			}
			if len(entries) != 0 {
				t.Fatalf("failure retained files: %v", entries)
			}
		})
	}
}

// TestStatFailureClosesDescriptor checks ownership on the post-open error path.
func TestStatFailureClosesDescriptor(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	}()
	if err = s.Put(t.Context(), "package.pkg", bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatal(err)
	}
	open := s.open
	var opened *faultStat
	s.open = func(name string) (storedFile, error) {
		f, e := open(name)
		if e != nil {
			return nil, e
		}
		t.Cleanup(func() { _ = f.Close() })
		opened = &faultStat{storedFile: f}
		return opened, nil
	}
	if _, err = s.Open(t.Context(), "package.pkg", 0, -1); !errors.Is(err, errDisk) {
		t.Fatal(err)
	}
	if opened == nil {
		t.Fatal("descriptor was not opened")
	}
	if opened.closeCalls != 1 || opened.closeErr != nil {
		t.Fatalf("descriptor close calls = %d, error = %v", opened.closeCalls, opened.closeErr)
	}
}
