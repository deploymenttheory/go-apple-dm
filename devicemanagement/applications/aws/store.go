package aws

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	sdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/internal/blobutil"
)

// Store retains private package objects in an existing S3 bucket. The caller
// configures the SDK client, credentials, endpoint, retries and bucket access policy.
type Store struct {
	client         *s3.Client
	bucket, prefix string
}

var _ applications.BlobStore = (*Store)(nil)

// New binds an official AWS SDK v2 client without making network requests.
func New(client *s3.Client, bucket, prefix string) (*Store, error) {
	if client == nil || strings.TrimSpace(bucket) == "" {
		return nil, applications.ErrInvalid
	}
	if prefix != "" {
		if err := blobutil.Key(prefix); err != nil {
			return nil, err
		}
	}
	return &Store{client: client, bucket: bucket, prefix: prefix}, nil
}

// Put creates an object conditionally. Large inputs use bounded, sequential
// multipart uploads. Failed multipart operations abort with an independent timeout.
func (s *Store) Put(ctx context.Context, key string, r io.ReadSeeker, size int64) error {
	object, err := blobutil.Object(s.prefix, key)
	if err != nil {
		return err
	}
	if err = blobutil.Input(ctx, r, size); err != nil {
		return err
	}
	const partSize = 16 << 20
	if size <= partSize {
		_, err = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &object, Body: r, ContentLength: &size, ContentType: sdk.String("application/octet-stream"), IfNoneMatch: sdk.String("*")})
		return normalize(err)
	}
	if size > partSize*10000 {
		return applications.ErrTooLarge
	}
	return s.multipart(ctx, object, r, size, partSize)
}

// multipart streams bounded S3 parts and aborts incomplete uploads on failure.
func (s *Store) multipart(ctx context.Context, key string, r io.Reader, size, partSize int64) (retErr error) {
	created, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: &s.bucket, Key: &key, ContentType: sdk.String("application/octet-stream")})
	if err != nil {
		return normalize(err)
	}
	if created.UploadId == nil || *created.UploadId == "" {
		return fmt.Errorf("%w: missing S3 upload ID", applications.ErrIntegrity)
	}
	completed := false
	defer func() {
		if !completed {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			_, e := s.client.AbortMultipartUpload(cleanup, &s3.AbortMultipartUploadInput{Bucket: &s.bucket, Key: &key, UploadId: created.UploadId})
			if e != nil {
				retErr = errors.Join(retErr, fmt.Errorf("abort S3 multipart upload: %w", e))
			}
		}
	}()
	buffer := make([]byte, partSize)
	parts := []types.CompletedPart{}
	var nextPart int32 = 1
	for remaining := size; remaining > 0; {
		count := min(remaining, partSize)
		if _, err = io.ReadFull(blobutil.Reader{Context: ctx, Input: r}, buffer[:count]); err != nil {
			return err
		}
		number := nextPart
		nextPart++
		uploaded, e := s.client.UploadPart(ctx, &s3.UploadPartInput{Bucket: &s.bucket, Key: &key, UploadId: created.UploadId, PartNumber: &number, Body: bytes.NewReader(buffer[:count]), ContentLength: &count})
		if e != nil {
			return normalize(e)
		}
		if uploaded.ETag == nil {
			return fmt.Errorf("%w: missing S3 part ETag", applications.ErrIntegrity)
		}
		parts = append(parts, types.CompletedPart{PartNumber: &number, ETag: uploaded.ETag, ChecksumCRC32: uploaded.ChecksumCRC32, ChecksumCRC32C: uploaded.ChecksumCRC32C, ChecksumSHA1: uploaded.ChecksumSHA1, ChecksumSHA256: uploaded.ChecksumSHA256})
		remaining -= count
	}
	_, err = s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{Bucket: &s.bucket, Key: &key, UploadId: created.UploadId, MultipartUpload: &types.CompletedMultipartUpload{Parts: parts}, IfNoneMatch: sdk.String("*")})
	completed = err == nil
	return normalize(err)
}

// Open reads the requested range. The caller closes the returned body.
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
	input := &s3.GetObjectInput{Bucket: &s.bucket, Key: &object}
	if offset != 0 || length != -1 {
		v := fmt.Sprintf("bytes=%d-", offset)
		if length > 0 {
			v += fmt.Sprint(offset + length - 1)
		}
		input.Range = &v
	}
	out, err := s.client.GetObject(ctx, input)
	if err != nil {
		return nil, normalize(err)
	}
	return out.Body, nil
}

// Delete removes the current object. Bucket version retention remains a caller policy.
func (s *Store) Delete(ctx context.Context, key string) error {
	object, err := blobutil.Object(s.prefix, key)
	if err != nil {
		return err
	}
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &object})
	err = normalize(err)
	if errors.Is(err, applications.ErrNotFound) {
		return nil
	}
	return err
}

// normalize preserves provider errors while exposing shared missing-object and conflict classifications.
func normalize(err error) error {
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return errors.Join(applications.ErrNotFound, err)
		case "PreconditionFailed", "ConditionalRequestConflict":
			return errors.Join(applications.ErrConflict, err)
		}
	}
	return err
}
