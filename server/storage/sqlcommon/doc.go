// Package sqlcommon implements storage.Store over database/sql once, for
// every SQL backend: a backend supplies a Dialect (placeholder style, row
// locking, upsert syntax, and its migration files) and an opened *sql.DB.
//
// # Why
//
// Three SQL backends with three copies of the same statements would drift
// apart, and the contract suite would find the drift late. Phase 4 of the
// plan of record (decision record 0012) puts the statements, the
// transaction shapes, the embedded per-dialect migrations, the batched
// Clear, and the paginated queries in one place, parameterised by a
// Dialect. The later phase 4 records land here too: certificate
// association history (0014), the push certificate store (0015),
// UserAuthenticate state (0016), export and import (0017), and sealing of
// secret columns through server/storage/crypt with a Rewrap that rotates keys in
// place (0013). Values are never concatenated into SQL; only fixed column
// names and placeholder lists are built.
//
// The package does not open connections or choose a driver; sqlite,
// postgres, and mysql do, and each proves the result with storagetest.
//
// # References
//
//   - Decision record 0012: docs/research/decisions/0012-sql-storage-backends.md
//   - Decision record 0013: docs/research/decisions/0013-secrets-at-rest.md
//   - Decision record 0014: docs/research/decisions/0014-cert-association-history.md
//   - Decision record 0015: docs/research/decisions/0015-push-cert-store.md
//   - Decision record 0016: docs/research/decisions/0016-user-authenticate-state.md
//   - Decision record 0017: docs/research/decisions/0017-enrollment-export-import.md
//   - Plan of record: docs/research/implementation_plan.md (phase 4)
//   - Threat model: docs/security/threat-model.md (Storage rows)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package sqlcommon
