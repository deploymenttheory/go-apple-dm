package gcp

import (
	"context"
	"errors"
	"io"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/internal/blobutil"
)

// Store retains private package objects using Google Cloud Storage.
type Store struct {
	bucket *storage.BucketHandle
	prefix string
}

var _ applications.BlobStore = (*Store)(nil)

// New uses an existing bucket and official SDK client. The caller owns client
// closure, credential selection, retry policy and bucket access configuration.
func New(client *storage.Client, bucket, prefix string) (*Store, error) {
	if client == nil || strings.TrimSpace(bucket) == "" {
		return nil, applications.ErrInvalid
	}
	if prefix != "" {
		if err := blobutil.Key(prefix); err != nil {
			return nil, err
		}
	}
	return &Store{bucket: client.Bucket(bucket), prefix: prefix}, nil
}

// Put writes a resumable object upload with bounded buffering and a generation
// precondition. Cancellation aborts an incomplete write before it is finalized.
func (s *Store) Put(ctx context.Context, key string, r io.ReadSeeker, size int64) error {
	object, err := blobutil.Object(s.prefix, key)
	if err != nil {
		return err
	}
	if err = blobutil.Input(ctx, r, size); err != nil {
		return err
	}
	upload, cancel := context.WithCancel(ctx)
	defer cancel()
	writer := s.bucket.Object(object).If(storage.Conditions{DoesNotExist: true}).NewWriter(upload)
	writer.ChunkSize = 4 << 20
	writer.ContentType = "application/octet-stream"
	n, err := io.Copy(writer, blobutil.Reader{Context: upload, Input: r})
	if err != nil || n != size {
		cancel()
		closeErr := writer.Close()
		if err == nil {
			err = applications.ErrIntegrity
		}
		return errors.Join(err, closeErr)
	}
	return normalize(writer.Close())
}

// Open reads a range through the SDK. The caller closes the returned reader.
func (s *Store) Open(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	object, err := blobutil.Object(s.prefix, key)
	if err != nil {
		return nil, err
	}
	if err = blobutil.Range(offset, length); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if length == 0 {
		return io.NopCloser(strings.NewReader("")), nil
	}
	r, err := s.bucket.Object(object).NewRangeReader(ctx, offset, length)
	return r, normalize(err)
}

// Delete removes the live object; configured version retention remains in effect.
func (s *Store) Delete(ctx context.Context, key string) error {
	object, err := blobutil.Object(s.prefix, key)
	if err != nil {
		return err
	}
	err = s.bucket.Object(object).Delete(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil
	}
	return normalize(err)
}

// normalize preserves provider errors while exposing shared missing-object and conflict classifications.
func normalize(err error) error {
	if errors.Is(err, storage.ErrObjectNotExist) {
		return errors.Join(applications.ErrNotFound, err)
	}
	var api *googleapi.Error
	if errors.As(err, &api) && api.Code == 412 {
		return errors.Join(applications.ErrConflict, err)
	}
	return err
}
