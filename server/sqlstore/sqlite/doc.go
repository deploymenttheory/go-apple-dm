// Package sqlite implements MDM storage with the pure Go modernc.org/sqlite
// driver.
//
// # Design
//
// The dialect supplies SQL syntax and embedded migrations to sqlcommon.
// Connections use WAL, foreign keys, a busy timeout and immediate write
// transactions to coordinate writers. Driver uniqueness failures map to
// storage.ErrConflict.
//
// The backend requires no external service and runs the common storage
// contracts, sealing/rotation checks and cross-backend migration tests. SQLite
// persistence still requires protected files, keys and backups for production
// use.
//
// # References
//
//   - Decision record 0005: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0005-storage-interfaces.md
//   - Decision record 0012: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0012-sql-storage-backends.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
//   - Decision record 0017: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0017-enrollment-export-import.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (Storage rows)
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package sqlite
