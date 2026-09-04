// Package mysql is the MySQL storage backend on go-sql-driver/mysql.
//
// # Why
//
// Phase 4 of the plan of record promises the same storage contract on four
// backends, and MySQL is the one many existing MDM deployments already run
// (decision record 0012). This package supplies the Dialect that sqlcommon
// needs and nothing else: the connection is forced to parse times in UTC,
// DATETIME(6) columns keep microseconds, and upserts use
// INSERT ... AS new ON DUPLICATE KEY UPDATE, which requires MySQL 8.0.19 or
// later. NormalizeDSN adds the parameters the store depends on, and
// IsUniqueViolation maps driver errors to storage.ErrConflict.
//
// All statement logic, migrations, and sealing live in sqlcommon; the
// contract is proven by storagetest, run against a real server through
// `make test-storage` (see `make testdb-up`).
//
// # References
//
//   - Decision record 0005: docs/research/decisions/0005-storage-interfaces.md
//   - Decision record 0012: docs/research/decisions/0012-sql-storage-backends.md
//   - Decision record 0013: docs/research/decisions/0013-secrets-at-rest.md
//   - Plan of record: docs/research/implementation_plan.md (phase 4)
//   - Threat model: docs/security/threat-model.md (Storage rows)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package mysql
