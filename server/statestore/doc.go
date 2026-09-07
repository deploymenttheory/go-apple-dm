// Package statestore persists atomic protocol byte records in SQLite, PostgreSQL
// and MySQL.
//
// # Design
//
// Security services use these records for grants, certificate associations,
// revocation and quota accounting across replicas. Transactions lock fixed
// shards in deterministic order before reading database time, bounding lock
// metadata and coordinating expiry decisions. The protocol-state schema has its
// own migrations. The caller owns the database pool and chooses serialization
// and namespaces through domain interfaces.
//
// # References
//
//   - https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0047-enrollment-authentication-and-optional-security-services.md
//   - state.Store
//   - server/sqlstore/sqlcommon
package statestore
