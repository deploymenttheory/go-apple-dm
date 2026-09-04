// Package storagetest is the contract every storage backend must satisfy:
// suites a backend's own test runs through RunAll with a constructor
// returning a fresh, empty store, and a Failing wrapper that injects
// errors by method name.
//
// # Why
//
// Decision record 0005 makes the storage interfaces the seam between the
// service and the database, which only holds if every backend behaves
// identically where the service cares: re-enrollment cleanup, NotNow
// backoff, pagination, concurrent access, and the later additions of
// certificate association history, push certificates, UserAuthenticate
// state, and export and import. Phase 2 of the plan of record delivers the
// suites with inmem, and phase 4 runs them against SQLite, PostgreSQL, and
// MySQL in CI, so a divergence is a failing test rather than a production
// surprise. Failing lets the service tests prove their own error paths
// without a broken database.
//
// The suites pin behaviour, not performance; the timing gate is a separate
// benchmark in the postgres package.
//
// # References
//
//   - Decision record 0005: docs/research/decisions/0005-storage-interfaces.md
//   - Decision record 0012: docs/research/decisions/0012-sql-storage-backends.md
//   - Decision record 0013: docs/research/decisions/0013-secrets-at-rest.md
//   - Decision record 0014: docs/research/decisions/0014-cert-association-history.md
//   - Decision record 0015: docs/research/decisions/0015-push-cert-store.md
//   - Decision record 0016: docs/research/decisions/0016-user-authenticate-state.md
//   - Decision record 0017: docs/research/decisions/0017-enrollment-export-import.md
//   - Plan of record: docs/research/implementation_plan.md (section 6, test strategy; phase 2)
//   - Threat model: docs/security/threat-model.md (Storage rows)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package storagetest
