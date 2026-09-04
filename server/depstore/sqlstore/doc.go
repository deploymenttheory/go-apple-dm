// Package sqlstore is the SQL dep.Store: one implementation over
// database/sql for the SQLite, PostgreSQL, and MySQL dialects.
//
// # Why
//
// The DEP client needs persistent accounts with their OAuth tokens, the
// shared session, the sync cursor with the time it was received, the
// token PKI keypairs, every device Apple reported, the profiles defined,
// and per-serial assignment outcomes, with the same semantics on every
// backend the MDM storage already supports. Phase 6 of the plan of record
// delivers it with the client (decision record 0026). This package reuses
// server/storage/sqlcommon's dialects and migration runner but owns its own
// migration set, recorded in dep_schema_migrations, so it can share a
// database with the MDM and DDM schemas or live apart. The OAuth secrets,
// session tokens, and private keys are sealed through server/storage/crypt under
// column-bound purposes when a keyring is given (record 0013); device and
// profile records are stored as the exact bytes dep.Marshal produces (no
// engine JSON types) beside indexed copies of the keys the assigner
// filters on; every timestamp is written and read in UTC; every list is
// keyset paginated; and a page of devices commits with its cursor in one
// transaction. The contract suite in dep/deptest runs against all three
// dialects.
//
// # References
//
//   - Decision record 0026: docs/research/decisions/0026-dep-client-sync-and-assignment.md
//   - Decision record 0012: docs/research/decisions/0012-sql-storage-backends.md (dialects and migrations)
//   - Decision record 0013: docs/research/decisions/0013-secrets-at-rest.md (sealed columns)
//   - Plan of record: docs/research/implementation_plan.md (section 5, DEP / ABM; phase 6)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/device
//   - Apple: https://developer.apple.com/documentation/devicemanagement/profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sync-devices (cursor lifetime)
//   - Migrations: dep/sqlstore/migrations/{sqlite,postgres,mysql}/0001_init.sql
package sqlstore
