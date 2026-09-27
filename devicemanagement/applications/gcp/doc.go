// Package gcp stores application packages in Google Cloud Storage through the Google Cloud SDK for Go.
//
// # Design
//
// New accepts a caller-configured Storage client and an existing private bucket.
// Put uses resumable uploads and a generation precondition to prevent replacement.
// Open supports byte ranges, and Delete is idempotent. The caller owns the client
// lifetime, credentials, bucket access policy and retention. applications.Manager
// verifies package signatures and reads uploaded bytes back before publishing a
// content revision. Cloud object metadata is not substituted for those checks.
//
// # References
//
//   - https://pkg.go.dev/cloud.google.com/go/storage
package gcp
