// Package storagetest defines the contract suites every MDM storage backend
// runs.
//
// # Design
//
// RunAll checks enrollment lifecycle and cleanup, command ordering/results,
// NotNow retry, pagination, concurrent writes, certificates, user authentication
// and migration. Failing provides deterministic method-level errors for service
// tests. The same suites run against in-memory, SQLite, PostgreSQL and MySQL
// stores. Performance is measured separately by backend benchmarks and the
// PostgreSQL Clear timing gate.
//
// # References
//
//   - Decision record 0005: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0005-storage-interfaces.md
//   - Decision record 0012: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0012-sql-storage-backends.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
//   - Decision record 0014: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0014-cert-association-history.md
//   - Decision record 0015: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0015-push-cert-store.md
//   - Decision record 0016: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0016-user-authenticate-state.md
//   - Decision record 0017: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0017-enrollment-export-import.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (Storage rows)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package storagetest
