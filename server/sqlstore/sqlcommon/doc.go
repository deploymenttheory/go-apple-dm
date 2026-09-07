// Package sqlcommon implements storage.Store over database/sql using a
// caller-selected dialect.
//
// # Design
//
// Shared queries and transaction shapes cover enrollment, command queues,
// certificate history, push certificates, user-authentication state and
// migration. Dialects supply fixed SQL syntax and migrations; data values use
// parameters. Selected secret columns are sealed through storage/crypt, and
// Rewrap rotates them in guarded batches.
//
// The driver packages open connections. Satellite domain stores reuse
// migration/dialect support while owning their own migration sets. Operations
// across different domain stores are not a distributed transaction.
//
// # References
//
//   - Decision record 0012: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0012-sql-storage-backends.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
//   - Decision record 0014: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0014-cert-association-history.md
//   - Decision record 0015: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0015-push-cert-store.md
//   - Decision record 0016: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0016-user-authenticate-state.md
//   - Decision record 0017: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0017-enrollment-export-import.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (Storage rows)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package sqlcommon
