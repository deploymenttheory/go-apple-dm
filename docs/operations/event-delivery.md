# Event capture, delivery and retry

SQL-backed reference servers capture projected events in `event_records` and retain
one `event_deliveries` row for each destination configured at capture time. Capture
is enabled whenever the application uses SQL, even with no audit or webhook sink.
Audit is a separate, optional queryable trail. Memory storage and the in-process
bus have no persistent event history.

## Guarantees and configuration

| Path | Guarantee and boundary |
|---|---|
| Participating local SQL mutation | `event.Run` coordinates the mutation and event capture in one transaction. Capture failure rolls back that operation. This does not span remote Apple calls or independently supplied stores. |
| Native audit on the same SQL pool | Audit append and delivery acknowledgment commit together. `DM_AUDIT_STORE` enables the trail; `DM_AUDIT_RETENTION` configures its age-based prune worker. |
| Native webhook or custom external audit store | At-least-once attempts. Native webhooks deduplicate `webhook-id`; custom audit stores deduplicate `EventID`. A lost response can repeat an accepted delivery. |
| Slog and direct bus subscribers | Ephemeral notification, including after-commit notifications in SQL applications. Queue rejection, expiry and shutdown can lose notifications without undoing committed SQL state. |
| In-memory application audit | Bounded asynchronous bus delivery. No restart recovery. Managed webhooks require SQL. |

[Native webhooks](webhooks.md) use managed subscriptions enabled by
`DM_WEBHOOKS_ENABLED`. They have an independent envelope, separately authorized sensitive
representations, encrypted captures, Standard Webhooks signatures and authenticated
payload references. `dmctl webhooks` manages their configuration, delivery and replay.
`DM_EVENT_WORKERS`, `DM_EVENT_QUEUE_CAPACITY` and `DM_EVENT_DELIVERY_TIMEOUT` affect
the in-memory bus, including slog, rather than persistent destination scheduling.
Persistent workers currently default to a ten-second attempt timeout and one-second
idle polling. They lease attempts independently across replicas. HTTP 408/429/5xx,
transport errors and timeouts retry with backoff capped at one hour; a larger
Retry-After can extend that delay up to twenty-four hours. Other HTTP rejections
and unavailable destination configurations become `blocked`. These are distinct
from database failures, which stop the worker and are visible in worker health.

On process cancellation, an unacknowledged lease remains available for recovery
after expiry. Delivery order across events is not guaranteed. A denial is recorded
separately after its local transaction rolls back; a recording failure cannot
authorize the denied request.

## Inspect and retry

Use the ordinary authenticated `dmctl` configuration. The commands are:

```bash
dmctl events status
dmctl events list --limit 100
dmctl events list --deliveries --state blocked --limit 100
dmctl events list --event-id EVENT_ID
dmctl events retry --event-id EVENT_ID --destination DESTINATION_ID
```

The equivalent routes are under `/admin/v1`:

| Method and route | Purpose |
|---|---|
| `GET /events/status` | Delivery counts, capture health and worker state |
| `GET /events` | Projected occurrences, including events without destinations |
| `GET /events/deliveries` | Destination state, attempt counts, next attempt and redacted failure code |
| `GET /events/{event}` | One original projected occurrence |
| `POST /events/{event}/retry` | Reset an eligible waiting/blocked delivery using `{"destination":"DESTINATION_ID"}` |

Reads require `readAudit`; retry requires `retryEvents` under the
existing admin authorization rules. Routes are registered only with a persistent
event store. `dmctl routes` reports the available surface.

Pages default to 100 items, with a maximum of 1,000. Event pages are ordered by
stable event ID, not by occurrence timestamp. Pass the last ID as `--after-event`
(`after_event` in HTTP); `--type` filters occurrences. Delivery pages are ordered by
event ID then destination and require both `--after-event` and
`--after-destination` from the last row. Delivery state accepts `pending`, `blocked`, `delivered`, `paused`, `expired`
or `cancelled`; the type filter applies only to occurrence listing.

Resolve the underlying rejection or missing destination before retry. Retry does
not reset delivered rows or active leases, and conflicting/ineligible requests
return HTTP 409. It does not create a new destination or replay a delivered record.

## Destination changes and retention

Native webhooks store opaque scheduling markers in this outbox and encrypted bodies
in their own tables. Use `dmctl webhooks` for retry/replay; the generic event retry
endpoint rejects native destinations so it cannot bypass sensitive-payload authority.
Native audit uses the destination ID `audit`.

Native webhook retention prunes its payloads and scheduling records independently.
The ordinary projected journal has no general retention worker; audit retention only
prunes the audit trail. Account for ordinary `event_records` and `event_deliveries`
in capacity and backup planning. Neither projection nor append-and-prune audit protects
against a database administrator modifying records.

Implementation evidence: [publisher](../../server/eventstore/publisher.go),
[worker](../../server/eventstore/worker.go), [queries and retry](../../server/eventstore/operations.go),
[application composition](../../server/internal/app/eventstore.go),
[admin routes](../../server/internal/app/adminevents.go) and
[CLI](../../server/internal/dmctl/eventverbs.go). The corresponding decisions are
[0037](../research/decisions/0037-event-sinks-and-redaction.md) and
[0038](../research/decisions/0038-persisted-audit-trail.md).
