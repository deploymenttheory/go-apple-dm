# 0012: SQL storage backends (SQLite, PostgreSQL, MySQL)

## Context

SQLite, PostgreSQL and MySQL require dialect-specific SQL while serving the same storage contracts.

## Decision

`server/sqlstore/sqlcommon` implements operations over `database/sql`. Dialects supply placeholders, locking, upserts, unique-violation mapping and embedded migrations. DSNs are validated by the selected driver; PostgreSQL parses configuration before connecting.

Command state is indexed by enrollment, state and sequence. `Next` reads open-state ranges, and `Clear` updates batches of up to 5,000 rows. Enrollment reset, certificate association and dependent user cleanup use transactions. Lists use keyset cursors. Timestamps are normalized to UTC.

Each domain owns a migration set with up and down sections. Declaration JSON is stored as bytes to preserve the content used for tokens.

The application is pre-release and has no existing database upgrade requirement.
The enrollment-replacement table therefore belongs in each MDM backend's
`0001_init.sql`; it does not introduce a separate upgrade migration.

## Rationale

Shared queries and contract suites limit behavioral differences between drivers. Separate migration sets allow the declaration engine and satellite stores to operate without the complete MDM schema.

## Constraints

Use a separate database, PostgreSQL schema, or SQLite file for isolation; table prefixes are not supported. Timing gates depend on database and runner resources. `STORAGE_TIMING=off` records performance without enforcing the threshold; the race build skips the timing assertion.

## Verification

All four backends run `storagetest`. SQL tests cover migrations, rollback, write failures, UTC round trips and batching. `make test-storage-perf` measures clearing 100,000 rows on PostgreSQL without the race detector.

## References

- [server/sqlstore](../../../server/sqlstore)
- [storage/storagetest](../../../storage/storagetest)
- <https://developer.apple.com/documentation/devicemanagement/check-in>
- <https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm@main`, `storage/mysql/schema.sql`, `storage/mysql/queue.go`, `storage/pgsql/schema.sql`, `storage/pgsql/queue.go`
- `jessepeterson/kmfddm@main`, `storage/mysql/schema.sql`
- `fleetdm/fleet`, `server/datastore/mysql/apple_mdm.go`, `ClearQueue`
