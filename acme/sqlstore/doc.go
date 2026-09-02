// Package sqlstore is the SQL acme.Store: one implementation over
// database/sql for the SQLite, PostgreSQL, and MySQL dialects.
//
// # Why
//
// The ACME server keeps no state of its own, so every property a device
// depends on is a property of the store, and a deployment that survives a
// restart needs those properties from a database rather than from a map.
// Phase 7 of the plan of record delivers the server against acme/inmem
// (decision record 0031); this package is the same contract where the
// records outlive the process. It reuses storage/sqlcommon's dialects and
// migration runner but owns its own migration set, recorded in
// acme_schema_migrations, so the ACME tables can share a database with the
// MDM, DDM, and DEP schemas or live apart.
//
// Nothing here is sealed. An account key is a public JWK, an attestation
// object is a signed statement the device sent in clear, and an issued
// certificate is published to whoever asks; there is no secret at rest for
// a keyring to protect, so Options carries none and the storage/crypt
// dependency the sibling stores have is absent.
//
// Each record is stored as the JSON of the whole value beside indexed
// copies of the fields that are looked up or filtered, so a record gains a
// field without a migration. Two rules are the store's own rather than
// SQL's: a duplicate account key and a second claim on a client identifier
// are decided by a unique index, never by reading first and writing after,
// because a read-then-write cannot be correct under concurrency; and a
// nonce is taken with DELETE ... RETURNING where the engine has it, and
// on MySQL by a read and a delete in one transaction whose row count says
// who won, so exactly one of two concurrent takers takes it. The contract
// suite in acme/acmetest runs against all three dialects.
//
// # References
//
//   - Decision record 0031: docs/research/decisions/0031-acme-server-and-state-store.md
//   - Decision record 0012: docs/research/decisions/0012-sql-storage-backends.md (dialects and migrations)
//   - Plan of record: docs/research/implementation_plan.md (section 9, phase 7)
//   - RFC 8555 (ACME): https://www.rfc-editor.org/rfc/rfc8555
//   - draft-ietf-acme-device-attest: https://datatracker.ietf.org/doc/draft-acme-device-attest/
//   - Migrations: acme/sqlstore/migrations/{sqlite,postgres,mysql}/0001_init.sql
package sqlstore
