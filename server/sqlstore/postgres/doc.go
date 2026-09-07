// Package postgres implements the MDM SQL storage dialect with pgx in
// database/sql mode.
//
// # Design
//
// The dialect supplies numbered placeholders, FOR UPDATE locks, ON CONFLICT
// upserts, TIMESTAMPTZ columns and unique-error classification. Open parses the
// DSN before connecting. Shared statements and sealing live in
// server/sqlstore/sqlcommon.
//
// Integration tests run the common storage contracts. The separate 100,000-row
// Clear timing gate runs without the race detector and can report timing without
// enforcement when STORAGE_TIMING=off.
//
// # References
//
//   - Decision record 0005: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0005-storage-interfaces.md
//   - Decision record 0012: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0012-sql-storage-backends.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (Storage rows)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package postgres
