// Package statestore persists atomic protocol state in SQLite, PostgreSQL and MySQL.
//
// # Why
//
// The reference server's security services must survive restarts and coordinate
// across replicas. Fixed lock shards, ordered before writes, bound lock metadata;
// database time governs expiry and quotas. The caller owns the database pool.
//
// # References
//
//   - docs/research/decisions/0047-enrollment-authentication-and-optional-security-services.md
//   - state.Store
//   - server/sqlstore/sqlcommon
package statestore
