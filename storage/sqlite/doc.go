// Package sqlite is the SQLite storage backend on modernc.org/sqlite (pure
// Go, no cgo).
//
// # Why
//
// Phase 4 of the plan of record promises the same storage contract on four
// backends, and SQLite is the one a single-binary deployment and the
// end-to-end suite run without a server (decision record 0012). This
// package supplies the Dialect that sqlcommon needs, the DSN builder, and
// the connection settings that make SQLite safe under concurrent writers:
// WAL journal, foreign keys, a busy timeout, and immediate write
// transactions so writers queue instead of failing. IsUniqueViolation maps
// driver errors to storage.ErrConflict. Because it needs no external
// service, the sealing, rewrap, and cross-backend migration tests for the
// whole SQL layer run here.
//
// All statement logic, migrations, and sealing live in sqlcommon; the
// contract is proven by storagetest.
//
// # References
//
//   - Decision record 0005: docs/research/decisions/0005-storage-interfaces.md
//   - Decision record 0012: docs/research/decisions/0012-sql-storage-backends.md
//   - Decision record 0013: docs/research/decisions/0013-secrets-at-rest.md
//   - Decision record 0017: docs/research/decisions/0017-enrollment-export-import.md
//   - Plan of record: docs/research/implementation_plan.md (phase 4)
//   - Threat model: docs/security/threat-model.md (Storage rows)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package sqlite
