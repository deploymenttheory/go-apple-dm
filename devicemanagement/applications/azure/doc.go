// Package azure stores application packages in Azure Blob Storage through the Azure SDK for Go.
//
// # Design
//
// New accepts a caller-configured Blob client and an existing private container.
// Put streams bounded blocks and conditionally commits a new blob. Open supports
// byte ranges, and Delete is idempotent. The caller owns credentials, container
// access policy and retention. applications.Manager verifies package signatures
// and reads uploaded bytes back before publishing a content revision.
//
// # References
//
//   - https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/storage/azblob
package azure
