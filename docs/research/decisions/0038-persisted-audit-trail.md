# 0038: The persisted audit trail

## Context

Operators need retained, attributable records of device and administrative activity with predictable retention and pagination.

## Decision

In SQL-backed reference applications, the event store captures projected occurrences
before asynchronous delivery. Participating local mutations and capture share a
transaction; native audit append and delivery acknowledgment share another
transaction on the same SQL pool. This makes audit delivery independent of the
in-memory bus's queue capacity and preserves pending work across restart. A custom
audit store using another pool is an external, at-least-once destination.

In-memory applications still use bounded asynchronous delivery: accepted events
retain context values independently of request cancellation, but can be lost on
overload, expiry or shutdown. See [event delivery](../../operations/event-delivery.md)
for status and retry operations.

`audit.Store` exposes append, query and age-based prune operations. Records contain projected fields from the event registry, event metadata and an actor string captured at the time. There is no update or delete-by-ID operation. IDs are not reused after pruning.

SQL backends share the application pool but own a migration set and query indexes. An optional worker applies configured age-based retention and retries failed prune operations. Retention is disabled when no duration is configured.

## Rationale

A shared contract defines ordering and cursor behavior across backends. Actor strings preserve attribution when a principal is removed. Separate migrations keep audit schema lifecycle independent of enrollment schema.

## Constraints

The API is append-and-prune; it is not a cryptographically tamper-evident log and cannot prevent a database administrator from editing rows. A failure to capture an event can roll back a participating local operation. Later
destination failures leave persistent delivery state for retry or operator action;
they do not roll back an already committed device operation. Denials are captured
separately after rollback, and capture failure cannot turn a denial into approval.
In-memory storage is lost on restart. Operators select audit retention and backup
policy. Audit pruning does not prune the separate event-record/delivery tables;
there is currently no event-store pruning API or retention worker.

## Verification

Audit suites cover append, filtered pagination, prune and non-reused IDs on all backends. SQL tests cover rollback. Application tests cover projected state changes, administrative actors, route access, retention timing and prune failures.

## References

- [server/eventstore](../../../server/eventstore)
- [server/internal/app/eventstore.go](../../../server/internal/app/eventstore.go)
- [server/audit](../../../server/audit)
- [server/internal/app/adminaudit.go](../../../server/internal/app/adminaudit.go)
- <https://developer.apple.com/documentation/devicemanagement/check-in>
- <https://developer.apple.com/documentation/devicemanagement/commands-and-queries>

Reference source identifiers and paths (relative to the named project):

- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a`
- `server/datastore/mysql/migrations/tables/20210709124443_CreateActivitiesTable.go`
- `server/service/activities.go`, `server/datastore/mysql/activities.go`
- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10`, `service/webhook/service.go`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107`, `workflow/webhook/webhook.go`
- `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151`, `notifier/notifier.go`
