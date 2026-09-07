// Package sqlstore persists administrative principals, token digests and
// policies in SQL.
//
// # Design
//
// SQLite, PostgreSQL and MySQL share the adminauth.Store contract and a separate
// adminauth_schema_migrations set. Credential digests support lookup, rotation
// and revocation without storing raw API tokens. Policies survive process
// restarts and use versions for cache invalidation. Records are not sealed with
// a keyring, so database authorization and backups remain deployment
// responsibilities.
//
// # References
//
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0034-admin-api-and-authorization.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (admin API)
//   - Contract suite: adminauth/adminauthtest
//   - Migration mechanics: server/storage/sqlcommon
package sqlstore
