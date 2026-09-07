// Package sqlstore persists device enrollment service accounts, devices,
// profiles and assignment state in SQL.
//
// # Design
//
// SQLite, PostgreSQL and MySQL use a separate dep_schema_migrations set with
// shared dialect helpers. OAuth secrets, sessions and private keys are sealed
// through storage/crypt when a keyring is supplied. Device/profile bytes are
// stored beside indexed lookup fields, timestamps use UTC and lists use keyset
// pagination. Device pages commit with their cursors in one transaction.
// appleplatformservices/dep/deptest supplies the shared contract tests.
//
// # References
//
//   - Decision record 0026: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0026-dep-client-sync-and-assignment.md
//   - Decision record 0012: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0012-sql-storage-backends.md (dialects and migrations)
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md (sealed columns)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/device
//   - Apple: https://developer.apple.com/documentation/devicemanagement/profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sync-devices (cursor lifetime)
//   - Migrations: dep/sqlstore/migrations/{sqlite,postgres,mysql}/0001_init.sql
package sqlstore
