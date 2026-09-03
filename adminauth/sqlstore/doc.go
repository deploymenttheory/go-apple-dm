// Package sqlstore is the SQL-backed adminauth.Store for SQLite, PostgreSQL,
// and MySQL.
//
// # Why
//
// Admin credentials have to be revocable without restarting the process, and
// policies have to survive one, so both live in the database rather than in
// configuration. Fleet's API-only tokens are the counter-example this exists
// to avoid: they never expire and there is no way to say otherwise.
//
// The schema is its own migration set, `adminauth_schema_migrations`, so the
// admin tables version independently of the MDM, DDM, DEP, and ACME sets, the
// same separation records 0020 and 0031 made. There is no keyring here: the
// only credential-shaped column holds a SHA-256 digest of a token, and a
// digest is not a secret. Sealing it would protect nothing and would add a
// strict-mode failure path on the authentication hot path.
//
// # References
//
//   - Decision record: docs/research/decisions/0034-admin-api-and-authorization.md
//   - Plan of record: docs/research/implementation_plan.md (phase 8)
//   - Threat model: docs/security/threat-model.md (admin API)
//   - Contract suite: adminauth/adminauthtest
//   - Migration mechanics: storage/sqlcommon
package sqlstore
