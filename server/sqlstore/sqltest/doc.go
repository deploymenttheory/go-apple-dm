// Package sqltest supplies large command-queue fixtures for SQL tests and
// benchmarks.
//
// # Design
//
// Seed inserts pending command rows in multi-row batches sized for the backend's
// parameter limits. This keeps fixture construction from dominating Clear
// performance measurements. The helpers bypass ordinary storage API behavior and
// are only for tests; they do not define a backend contract.
//
// # References
//
//   - Decision record 0012: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0012-sql-storage-backends.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device
package sqltest
