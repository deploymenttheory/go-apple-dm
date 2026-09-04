// Package sqlstore is the SQL ddm.Store: one implementation over
// database/sql for the SQLite, PostgreSQL, and MySQL dialects.
//
// # Why
//
// The engine needs persistent declarations, versions, sets, assignments,
// per-enrollment snapshots, status, and the change queue, with the same
// semantics on every backend the MDM storage already supports. This package
// reuses storage/sqlcommon's dialects and migration runner but owns its own
// migration set, recorded in ddm_schema_migrations, so it can share a
// database with the MDM schema or live in a separate one; nothing references
// the enrollments table. Every byte column holds the exact bytes given (no
// JSON column types, so tokens hash what was stored), every timestamp is
// written and read in UTC, every list is keyset paginated, and change rows
// are written inside the mutating transaction. The contract suite in
// ddm/ddmtest runs against all three dialects.
//
// # References
//
//   - Decision record 0020: docs/research/decisions/0020-ddm-engine-membership-and-storage.md
//   - Decision record 0021: docs/research/decisions/0021-status-reports-and-subscriptions.md
//   - Decision record 0022: docs/research/decisions/0022-change-notifier.md
//   - Decision record 0012: docs/research/decisions/0012-sql-storage-backends.md (dialects and migrations)
//   - Plan of record: docs/research/implementation_plan.md (phase 5)
//   - Migrations: ddm/sqlstore/migrations/{sqlite,postgres,mysql}/0001_init.sql
package sqlstore
