// Package aws stores application packages in Amazon S3 through the AWS SDK for Go v2.
//
// # Design
//
// New accepts a caller-configured S3 client and an existing private bucket.
// Put uses conditional creation and bounded multipart uploads; failed multipart
// operations are aborted. Open supports byte ranges, and Delete is idempotent.
// The caller owns credentials, bucket access policy and object retention.
// applications.Manager verifies package signatures and reads uploaded bytes back
// before publishing a content revision. S3 ETags are not treated as package hashes.
//
// # References
//
//   - https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/s3
package aws
