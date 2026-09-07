// Package sqlstore persists ddm.Store records in SQLite, PostgreSQL and MySQL.
//
// # Design
//
// The store owns ddm_schema_migrations while reusing server/sqlstore/sqlcommon
// dialects and migration handling. It persists declarations, versions, sets,
// assignments, snapshots, status and pending changes. Exact byte columns
// preserve hashed content, timestamps use UTC and lists use keyset pagination.
// Mutations and their notification rows share a transaction.
//
// No foreign key points to MDM enrollment tables, allowing a separate engine
// database. Lifecycle cleanup is coordinated by server/ddmsync.
// storage/ddm/ddmtest supplies the contract suite.
//
// # References
//
//   - Decision record 0020: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0020-ddm-engine-membership-and-storage.md
//   - Decision record 0021: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0021-status-reports-and-subscriptions.md
//   - Decision record 0022: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0022-change-notifier.md
//   - Decision record 0012: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0012-sql-storage-backends.md (dialects and migrations)
//   - Migrations: ddm/sqlstore/migrations/{sqlite,postgres,mysql}/0001_init.sql
package sqlstore
