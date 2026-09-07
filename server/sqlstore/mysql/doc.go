// Package mysql implements the MDM SQL storage dialect with go-sql-driver/mysql.
//
// # Design
//
// The dialect supplies placeholders, locking, unique-error classification and
// embedded migrations to sqlcommon. NormalizeDSN enables parsed UTC times;
// DATETIME(6) preserves microseconds. Upserts use INSERT ... AS new ON DUPLICATE
// KEY UPDATE and require MySQL 8.0.19 or later.
//
// Shared statement logic and sealing live in server/sqlstore/sqlcommon.
// Integration tests run storage/storagetest against a configured database
// through make test-storage.
//
// # References
//
//   - Decision record 0005: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0005-storage-interfaces.md
//   - Decision record 0012: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0012-sql-storage-backends.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (Storage rows)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package mysql
