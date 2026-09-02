# 0012: SQL storage backends (SQLite, PostgreSQL, MySQL)

Status: accepted
Date: 2026-09-01
Phase: 4

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> (what an enrollment must retain)
- Doc: <https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses>

## References read

- `micromdm/nanomdm@main` `storage/mysql/schema.sql`, `storage/mysql/queue.go`, `storage/pgsql/schema.sql`, `storage/pgsql/queue.go`
- `jessepeterson/kmfddm@main` `storage/mysql/schema.sql`
- `fleetdm/fleet` `server/datastore/mysql/apple_mdm.go` (nano_* tables, `ClearQueue` rewrite)

## Known pitfalls found

- NanoMDM #260: `ClearQueue` on PostgreSQL is row-by-row through a view join with no state index.
- NanoMDM #53: SQL views in the schema complicated migrations and were removed.
- NanoMDM #71: bootstrap and unlock tokens survive re-enrollment because `StoreAuthenticate` does not clear them.
- NanoMDM #86: bootstrap tokens live on the device table with no migration path between backends.
- NanoMDM #64: table prefixes requested for shared databases.
- Timestamps without a timezone (`TIMESTAMP` in MySQL and PostgreSQL) shift with the server timezone; `time.Time` round trips must be UTC.
- MySQL `RowsAffected` counts changed rows unless `clientFoundRows` is set; an identical re-sent TokenUpdate looked like a missing enrollment.
- NanoMDM's `Disable` is `WHERE device_id = ?` so user channels go with the device; the first cut disabled one row.

## What they do

- **NanoMDM**: `devices`, `users`, `enrollments`, `commands`, `command_results`, `enrollment_queue` plus a `view_queue`; results keyed by (enrollment, command); NotNow tally on the result row; separate hand-written MySQL and PostgreSQL packages with duplicated queries; schema applied by hand from `schema.sql`.
- **KMFDDM**: MySQL only, schema by hand, status values in flat tables.
- **Fleet**: MySQL only, goose-style migrations, `nano_*` tables mirrored from NanoMDM.

## What we do better

1. One implementation in `storage/sqlcommon` over `database/sql` with a small `Dialect` (placeholders, row locking, upsert syntax, migrations); the SQLite, PostgreSQL, and MySQL packages are drivers plus a migration directory, so the contract suite proves identical behaviour on all four backends. The PostgreSQL backend parses the DSN with `pgx.ParseConfig` before connecting, so a malformed DSN is an `Open` error rather than a first-query error.
2. Commands carry their delivery state on the row (`pending`, `sent`, `not-now`, terminal states) with an index on `(enrollment_id, state, seq)`. `Next` reads at most three index ranges on `(enrollment_id, state, seq)`, one per open state, so cost is proportional to the enrollment's open commands rather than the table; `Clear` is a batched indexed update (`ClearBatchSize` is 5,000 rows per statement) rather than a view join.
3. Re-enrollment is one transaction: the enrollment row is reset, pending commands are cleared, the certificate pin and bootstrap token are dropped, and the device's user channels are disabled.
4. Embedded, versioned migrations with up and down sections applied by the library (`sqlcommon.Migrate`, `sqlcommon.Rollback`); no hand-applied schema files.
5. Every timestamp column is timezone-aware (`TIMESTAMPTZ`, `DATETIME(6)` with a UTC connection, SQLite text with offset) and normalised to UTC on read.
6. Keyset pagination on `List` (by id) and `Commands` (by sequence) rather than offsets.
7. Unique violations are mapped to `storage.ErrConflict` per dialect (`Dialect.IsUniqueViolation`), so races for the same certificate hash never surface a driver error.
8. Migrations are per `MigrationSet` (table plus files); the DDM package will own `ddm_schema_migrations` and stores declaration JSON as bytes so all dialects stay identical.
   Phase 5 note: declaration JSON is stored as bytes, never as an engine JSON type, so hashing and round trips are byte-identical on every backend.

Schema note: the `enrollments` table in `0001_init.sql` also carries `device_topic`, `token_update_raw`, `cert_hash_at`, and `bootstrap_token_at`, plus the `cert_associations`, `push_certs`, and `user_auth` tables, because records 0013 to 0017 landed in the same phase; the sealed columns are listed in 0013.

## Verified by

1. `sqlite.TestContract`, `postgres.TestContract`, `mysql.TestContract` (all run `storagetest.RunAll`); the e2e scenarios on `E2E_STORE=sqlite` and `E2E_STORE=postgres`.
2. `sqlite.TestClearBatches`, `storagetest.RunCommandQueueSuite/OrderAndResults` (NotNow backoff), `storagetest.RunCommandQueueSuite/ClearFilter`, `postgres.TestClear100kUnderOneSecond` (measured 0.86s on PostgreSQL 17 in Docker without the race detector, run by `make test-storage-perf`; the timing assertion is skipped under the race detector and downgraded to a log line with `STORAGE_TIMING=off`), `postgres.BenchmarkClear100k`, `sqlite.BenchmarkClear100k` (0.50s on SQLite).
3. `storagetest.RunEnrollmentSuite/ReenrollClearsState`, `storagetest.RunEnrollmentSuite/DisableCascadesToUserChannels`, `storagetest.RunEnrollmentSuite/IdempotentTokenUpdateSameInstant` on every backend.
4. `sqlcommon.TestMigrateAndRollback`, `sqlcommon.TestMigrationParsing`.
5. `storagetest.RunEnrollmentSuite/Lifecycle` (`Equal` on times after a round trip) on every backend.
6. `storagetest.RunEnrollmentSuite/ListPagination` on every backend, `sqlite.TestClearBatches` (keyset paging over commands).
7. `storagetest.RunConcurrencySuite/CertPinRace` (exactly one nil, the rest `ErrConflict`, never a raw driver error), `sqlite.TestIsUniqueViolation`, `postgres.TestIsUniqueViolation`, `mysql.TestIsUniqueViolation`.
8. `sqlcommon.TestMigrationSetTable`, `sqlcommon.TestDialectMigrationsAgree` (all three embedded directories declare the same versions and names, every file has a down section).

## Rejected alternatives

- `pressly/goose` as a library: the migration loop is 100 lines and gated at 95%; goose adds a dependency surface for a feature we would only partly use.
- Separate query sets per backend (NanoMDM): three copies of every bug.
- Offset pagination: unstable under concurrent inserts.
- Storing the full response as JSON: `mdm.Response.Payload` is an interface; the raw plist is kept instead and decodes back through `mdm.DecodeResponse`.
- Table prefixes (NanoMDM #64): use a PostgreSQL schema via `search_path` (the e2e harness does this per test), a MySQL database, or a separate SQLite file; a prefix would template the plain SQL migration files and touch every query literal.
- `UnlockTokenAt`: `TokenUpdatedAt` already records the instant the token arrives.
