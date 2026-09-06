# 0038: The persisted audit trail

Status: accepted
Date: 2026-09-03
Phase: 9

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> and
  <https://developer.apple.com/documentation/devicemanagement/commands-and-queries> — the state
  changes worth recording are the protocol's, so the vocabulary of the trail is the event types
  service, ddm, push, acme and dep already publish.
- YAML: `third_party/device-management/mdm/checkin/tokenupdate.yaml` — `UnlockToken`, the reason a
  trail must store the projection from record 0037 rather than the payload.

Framing: Apple documents no audit surface. An MDM server that can erase a fleet needs one anyway,
and the threat model has claimed since phase 0 that "every state change emits an event, and
subscribers persist them" while no subscriber existed. This record is the second half of making
that true; record 0037 was the first.

Dependency note: none. The store reuses `storage/sqlcommon`'s migration runner and the dialect
values the storage backends already define.

## References read

- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a`
  `server/datastore/mysql/migrations/tables/20210709124443_CreateActivitiesTable.go`,
  `server/service/activities.go`, `server/datastore/mysql/activities.go`
- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10` `service/webhook/service.go`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107` `workflow/webhook/webhook.go`
- `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151` `notifier/notifier.go`

## Known pitfalls found

- `fleet/.../20210709124443_CreateActivitiesTable.go:15-24`: the `activities` table is indexed on
  its primary key alone. A feed that only ever grows, queried by time, has no index on `created_at`,
  so every "what happened last Tuesday" is a scan.
- Same file, lines 33-35: the down migration is `return nil`. The table is never dropped on
  rollback, so a reverted deployment leaves the schema behind and the next migration up sees a table
  it did not create.
- Same file, line 22: `user_id` is a foreign key with `ON DELETE SET NULL`. Deleting an
  administrator nulls the link on every action they took. `user_name` is denormalised beside it so
  the name survives, which is the right instinct, but the record of *which account* acted does not.
- `nanomdm/service/webhook/service.go` and `micromdm/workflow/webhook/webhook.go`: neither persists
  anything at all. The trail is whatever the operator's webhook receiver chose to keep, so
  answering a question after the fact depends on infrastructure the MDM server does not control and
  cannot verify.
- `kmfddm/notifier/notifier.go`: no webhook and no audit feed; DDM status is observable only through
  the API, and only in its current state rather than as a history.
- Ours: `docs/security/threat-model.md` has asserted the repudiation control since phase 0, and
  `internal/app/adminauthz.go` publishes `AdminAction` and `AdminDenied` into a bus with no
  subscribers. The control has been documented and unmet for the whole project.

## What they do

- **Fleet**: an `activities` table with `activity_type` and a JSON `details` blob, written through
  a service method, surfaced as an activity feed and optionally forwarded to a webhook. It is the
  only surveyed project that persists at all, and the closest prior art. Attribution is a user
  foreign key plus a denormalised name; there is no retention policy in the schema.
- **NanoMDM, MicroMDM**: no persistence. Events are POSTed to a receiver and forgotten. NanoMDM can
  HMAC the body; neither retries, so a receiver that is briefly down loses the record permanently.
- **KMFDDM**: nothing.

## What we do better

1. **The trail is append-and-prune by interface.** There is no update and no delete by id. A trail
   whose rows can be edited answers no question worth asking, and the only removal is by age, so
   retention is a stated policy rather than a way to lose one inconvenient record.
2. **What is stored is the projection, never the payload.** `Fields` holds what record 0037's
   registry allowed through, so the trail cannot become a long-lived copy of every device's
   `UnlockToken`. Fleet's `details` blob is whatever the caller passed.
3. **Indexed for the questions it exists to answer.** `(at DESC, id DESC)` for the feed and the
   prune, and `(enrollment_id, id DESC)`, `(actor, id DESC)`, `(type, id DESC)` for "what happened
   to this device", "what did this credential do", and "show me every erase". Fleet indexes none of
   these.
4. **Ids are never reused after a prune**, so a cursor held across a retention pass cannot silently
   start replaying different records. SQLite needs `AUTOINCREMENT` rather than a bare
   `INTEGER PRIMARY KEY` for this, which is the kind of detail a contract suite catches and a
   reviewer does not.
5. **A reversible migration.** The down direction drops what the up direction created, where
   Fleet's is a no-op.
6. **One contract across four backends.** In-memory, SQLite, PostgreSQL and MySQL pass the same
   suite, including cursor pagination *through a filter*, which is where a hand-written SQL WHERE
   and an in-memory loop usually stop agreeing.
7. **Retention is a supervised worker on the injected clock**, so it is asserted by a test rather
   than by waiting, and a failed prune is logged and retried instead of stopping the server's
   workers.
8. **Attribution survives.** The actor is a string written at the time, not a foreign key, so
   deleting an admin principal cannot blank the record of what they did. `break-glass` is a fixed
   actor for exactly this reason (record 0034, amendment 1).

## Verified by

1. The `audit.Store` interface has no update or delete; `audittest.RunSuite/Prune` covers the only
   removal path (claim 1).
2. `app.TestAuditTrailRecordsStateChanges`, and record 0037's `sink.TestNoSecretSurvivesProjection`,
   which is what the trail stores (claim 2).
3. Schema review; the indexes are in `audit/sqlstore/migrations/*/0001_init.sql` and the filters
   they serve are covered by `audittest.RunSuite/Filter` (claim 3).
4. `audittest.RunSuite/Prune/IDsAreNotReused` (claim 4; would fail on a bare SQLite
   `INTEGER PRIMARY KEY`).
5. `sqlstore.TestMigrations/VersionAndRollback` asserts the rollback reverts exactly one migration
   (claim 5; would fail on Fleet's no-op down).
6. `inmem.TestContract`, `sqlstore.TestContract`, `sqlstore.TestStorePostgres`,
   `sqlstore.TestStoreMySQL`, all running `audittest.RunSuite`, whose
   `Pagination/FilterSurvivesPaging` is the case that catches a filter dropped at a page boundary
   (claim 6).
7. `app.TestAuditRetentionPrunesOnItsInterval`, `app.TestAuditRetentionOffKeepsEverything`,
   `app.TestAuditRetentionSurvivesAFailedPrune` (claim 7).
8. `app.TestAuditTrailAttributesAdminRequests` (claim 8).

Cross-cutting: `app.TestAuditRouteListsAndPages`, `app.TestAuditRouteRejectsBadInput`,
`app.TestAuditRouteMapsStoreErrors`, `app.TestAuditRouteAbsentWithoutATrail`, and
`dmctl.TestAuditVerb`.

## Rejected alternatives

- Reusing `storage`'s migration set: the trail would then be coupled to the enrollment schema's
  version sequence, and a deployment that wants the trail without the rest could not have it.
  Records 0031 and 0034 set the precedent for a satellite store owning its own set.
- Storing `event.Data` as JSON: the shortest path and the one that puts `UnlockToken` in a table
  that is kept for months. Record 0037 exists to prevent exactly this.
- An `UpdateRecord` or `Delete(id)` for redaction requests: a trail that can be edited is not
  evidence. A deployment with a legal erasure obligation reduces the retention window, which is
  visible in configuration rather than performed row by row.
- A foreign key from the record to the admin principal, as Fleet has: deleting a principal would
  then blank the attribution, which is the opposite of what the trail is for.
- Retention as a `DELETE` on a size cap rather than an age: a burst of activity would silently
  evict the older records an investigation wants, and "how far back does this go" would have no
  answer.
- A separate audit database or an append-only file: an operator would have two backup and restore
  stories. The trail rides the pool the process already has.
- Writing the record synchronously in the service layer: the bus already carries every state
  change, and a storage failure would then fail a device check-in rather than losing a log line.
