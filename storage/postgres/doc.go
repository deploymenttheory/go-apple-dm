// Package postgres is the PostgreSQL storage backend on pgx in database/sql
// mode.
//
// # Why
//
// Phase 4 of the plan of record promises the same storage contract on four
// backends, and PostgreSQL is the one the timing gate runs on: Clear on
// 100k queued rows must finish under a second (decision record 0012).
// This package supplies the Dialect that sqlcommon needs and nothing
// else: $n placeholders, FOR UPDATE row locks, ON CONFLICT upserts, and
// TIMESTAMPTZ columns. IsUniqueViolation maps driver errors to
// storage.ErrConflict.
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
package postgres
