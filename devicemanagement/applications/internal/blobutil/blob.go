// Package blobutil validates object storage inputs shared by the applications adapters.
package blobutil

import (
	"context"
	"fmt"
	"io"
	"math"
	"path"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
)

// Key rejects traversal, ambiguous separators and nonportable object names.
func Key(key string) error {
	if key == "" || len(key) > 512 || path.Clean(key) != key || strings.HasPrefix(key, "/") {
		return applications.ErrInvalid
	}
	for _, part := range strings.Split(key, "/") {
		if part == "." || part == ".." || part == "" {
			return applications.ErrInvalid
		}
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_/.", c)) {
			return applications.ErrInvalid
		}
	}
	return nil
}

// Object joins a validated optional prefix and object key.
func Object(prefix, key string) (string, error) {
	if err := Key(key); err != nil {
		return "", err
	}
	if prefix == "" {
		return key, nil
	}
	if err := Key(prefix); err != nil {
		return "", err
	}
	return prefix + "/" + key, nil
}

// Input verifies the declared size and rewinds a seekable source before upload.
func Input(ctx context.Context, r io.ReadSeeker, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || size < 0 {
		return applications.ErrInvalid
	}
	n, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("%w: declared upload size", applications.ErrIntegrity)
	}
	_, err = r.Seek(0, io.SeekStart)
	return err
}

// Range validates bounds without knowing the object size. Providers check EOF.
func Range(offset, length int64) error {
	if offset < 0 || length < -1 || length > 0 && offset > math.MaxInt64-length {
		return applications.ErrInvalid
	}
	return nil
}

// Reader checks cancellation between reads without taking ownership of its source.
type Reader struct {
	Context context.Context
	Input   io.Reader
}

// Read checks caller cancellation before consuming source bytes.
func (r Reader) Read(p []byte) (int, error) {
	if err := r.Context.Err(); err != nil {
		return 0, err
	}
	return r.Input.Read(p)
}
