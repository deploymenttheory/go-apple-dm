package azure

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/internal/blobutil"
)

// Store retains package objects in an existing private Azure Blob container.
type Store struct {
	client            *azblob.Client
	container, prefix string
}

var _ applications.BlobStore = (*Store)(nil)

// New binds an official Azure SDK client. The caller configures authentication,
// retry policy and container access; this constructor does not contact Azure.
func New(client *azblob.Client, container, prefix string) (*Store, error) {
	if client == nil || strings.TrimSpace(container) == "" {
		return nil, applications.ErrInvalid
	}
	if prefix != "" {
		if err := blobutil.Key(prefix); err != nil {
			return nil, err
		}
	}
	return &Store{client: client, container: container, prefix: prefix}, nil
}

// Put streams blocks using bounded buffers and creates the blob only if absent.
func (s *Store) Put(ctx context.Context, key string, r io.ReadSeeker, size int64) error {
	object, err := blobutil.Object(s.prefix, key)
	if err != nil {
		return err
	}
	if err = blobutil.Input(ctx, r, size); err != nil {
		return err
	}
	etag := azcore.ETag("*")
	contentType := "application/octet-stream"
	_, err = s.client.UploadStream(ctx, s.container, object, blobutil.Reader{Context: ctx, Input: r}, &azblob.UploadStreamOptions{
		BlockSize: 4 << 20, Concurrency: 2, HTTPHeaders: &blob.HTTPHeaders{BlobContentType: &contentType},
		AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{IfNoneMatch: &etag}},
	})
	return normalize(err)
}

// Open reads a range through the SDK. The caller closes the response body.
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
	count := length
	if count < 0 {
		count = 0
	}
	out, err := s.client.DownloadStream(ctx, s.container, object, &azblob.DownloadStreamOptions{Range: blob.HTTPRange{Offset: offset, Count: count}})
	if err != nil {
		return nil, normalize(err)
	}
	return out.Body, nil
}

// Delete is idempotent for an absent blob. It does not alter container policy.
func (s *Store) Delete(ctx context.Context, key string) error {
	object, err := blobutil.Object(s.prefix, key)
	if err != nil {
		return err
	}
	_, err = s.client.DeleteBlob(ctx, s.container, object, nil)
	if bloberror.HasCode(err, bloberror.BlobNotFound) {
		return nil
	}
	return normalize(err)
}

// normalize preserves provider errors while exposing shared missing-object and conflict classifications.
func normalize(err error) error {
	if bloberror.HasCode(err, bloberror.BlobNotFound) {
		return errors.Join(applications.ErrNotFound, err)
	}
	if bloberror.HasCode(err, bloberror.ConditionNotMet, bloberror.BlobAlreadyExists) {
		return errors.Join(applications.ErrConflict, err)
	}
	return err
}
