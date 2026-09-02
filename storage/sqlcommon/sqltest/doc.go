// Package sqltest holds helpers for SQL backend tests and benchmarks that
// need large fixtures written faster than the storage API allows.
//
// # Why
//
// The phase 4 exit criterion that Clear on 100k queued rows finishes under
// a second on PostgreSQL (decision record 0012) needs 100k rows first, and
// enqueueing them one at a time through the store would dominate the
// benchmark. Seed inserts pending commands for an enrollment with
// multi-row INSERTs sized to stay under PostgreSQL's parameter limit,
// through the backend's own Dialect.
//
// It is not part of the storage contract: nothing here is a behaviour a
// backend must have, and only the sqlite and postgres tests import it.
//
// # References
//
//   - Decision record 0012: docs/research/decisions/0012-sql-storage-backends.md
//   - Plan of record: docs/research/implementation_plan.md (phase 4)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device
package sqltest
