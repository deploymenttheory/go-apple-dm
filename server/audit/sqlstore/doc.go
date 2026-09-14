// Package sqlstore implements persistent audit storage for SQLite, PostgreSQL
// and MySQL.
//
// Open wraps a database pool supplied by the caller and applies the audit
// migrations unless SkipMigrate is set. The caller owns the pool and must close
// it. Audit migration versions are tracked separately from other server tables.
//
// Records use database-generated identifiers for pagination. Lists return the
// newest records first, and appending an existing nonempty event ID returns the
// stored record instead of creating a duplicate.
//
// See the audit design decision:
// https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0038-persisted-audit-trail.md
package sqlstore
