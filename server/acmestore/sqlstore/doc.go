// Package sqlstore persists acme.Store records in SQLite, PostgreSQL and MySQL.
//
// # Design
//
// The implementation reuses server/sqlstore/sqlcommon dialects and migrations
// and owns acme_schema_migrations. JSON records retain protocol values beside
// indexed lookup fields. Unique constraints coordinate account-key registration
// and one-time identifier claims; transactional nonce removal permits only one
// consumer. Attestation bytes round-trip unchanged for re-verification at
// finalize.
//
// The store does not seal records with a keyring. Public keys and certificates
// do not contain private key material, but protocol and attestation records
// still require database access controls. storage/acme/acmetest defines the
// shared behavior.
//
// # References
//
//   - Decision record 0031: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0031-acme-server-and-state-store.md
//   - Decision record 0012: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0012-sql-storage-backends.md (dialects and migrations)
//   - RFC 8555 (ACME): https://www.rfc-editor.org/rfc/rfc8555
//   - draft-ietf-acme-device-attest: https://datatracker.ietf.org/doc/draft-acme-device-attest/
//   - Migrations: acme/sqlstore/migrations/{sqlite,postgres,mysql}/0001_init.sql
package sqlstore
