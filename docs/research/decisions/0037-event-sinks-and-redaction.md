# 0037: Event sinks and default-deny redaction

## Context

Events can contain protocol messages with escrowed secrets and private device data. External logs and webhooks need explicit field selection.

## Decision

A projection registry selects fields for each event type. Unknown types emit metadata only. The slog sink, webhook and persisted audit trail consume projected records. Webhooks use the MicroMDM-compatible envelope while omitting `raw_payload`.

SQL-backed reference applications capture projected records and destination IDs in
`server/eventstore`. Participating local mutations and capture commit together;
workers deliver audit/webhook records using persistent leases and retries. Native
audit append/acknowledgment shares the same SQL transaction. External delivery is
at least once and receivers deduplicate EventID. Slog and direct bus subscribers
remain ephemeral. 

Webhook construction requires HTTPS and rejects URL credentials and fragments.
Redirects are refused and response reads are bounded. `DM_WEBHOOK_ROOT_CA_FILE`
supplies a private CA bundle without disabling hostname verification. Transport
error strings omit the configured URL, including sensitive path/query values;
trusted callers can still inspect wrapped causes. Persistent worker retries own
delivery scheduling; they do not stack the webhook helper's in-memory retry loop.

In-memory applications use the asynchronous event bus for audit/webhook delivery.
The reference bus defaults to eight workers, a 1,024-event pending queue and a
30-second lifetime from acceptance. `DM_EVENT_WORKERS`, `DM_EVENT_QUEUE_CAPACITY`
and `DM_EVENT_DELIVERY_TIMEOUT` configure this bus, not SQL destination leases or
retry deadlines. Zero selects defaults; negative values fail configuration.
Saturation rejects new events without waiting, counts each rejection and limits
overflow warnings to one per ten seconds. `/admin/v1/config` and `dmctl status`
expose bus statistics; `dmctl events status` exposes persistent capture/delivery.

Accepted bus events preserve context values independently of request cancellation.
Subscribers run in registration order for each event; different events can finish
out of order. Expired queued events are not delivered. Closing an owned bus drains
for five seconds before cancellation; injected buses remain caller-owned. SQL
workers leave unacknowledged leases for recovery after expiry, rather than deleting
pending deliveries during shutdown.

Registering a nil projection removes any prior field projection and retains the
type as known. Future lookups emit metadata only; an in-flight projection can
finish using its captured function.

## Rationale

An allowlist keeps new fields from leaving the process unless they are reviewed. A common projection gives every sink the same disclosure policy. Asynchronous delivery separates receiver latency from device request handling.

## Constraints

In-memory audit/webhook handlers share bus capacity and can lose events through
overload, expiry, handler failure or shutdown. The bus bounds event count, not
payload bytes; non-cooperating subscribers can retain a worker. SQL delivery state
survives restart but does not guarantee global ordering, unlimited retention policy
or exactly-once network delivery. A changed destination is not a request to replay
old events to it. Capture failures can roll back participating local mutations;
after-commit bus notification failures cannot undo the commit.

Direct internal subscribers can receive unprojected data and must enforce their
own disclosure policy. MicroMDM receivers requiring `raw_payload` are not
byte-for-byte compatible. Projection is not encryption or database tamper evidence.

## Verification

Projection tests seed payloads with sentinel secrets, cover every event type and reject mismatched payload types. Sink tests cover the envelope, retries, cancellation, failed destinations and redaction; application tests cover event delivery and close/drain behavior. Event-store suites cover transactional rollback, restart, leases, destination isolation and manual retry across SQL backends.

## References

- [server/eventstore](../../../server/eventstore)
- [server/internal/app/eventstore.go](../../../server/internal/app/eventstore.go)
- [server/eventsink](../../../server/eventsink)
- [mdmprotocol/event](../../../devicemanagement/mdmprotocol/event)
- [server/internal/app/app.go](../../../server/internal/app/app.go)
- <https://developer.apple.com/documentation/devicemanagement/check-in>
- <https://developer.apple.com/documentation/devicemanagement/tokenupdate>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10`, `service/webhook/service.go`
- `service/webhook/event.go`, `service/webhook/event.json`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107`, `workflow/webhook/webhook.go`
- `workflow/webhook/checkin.go`, `workflow/webhook/acknowledge.go`, `workflow/webhook/http_post.go`
- `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151`, `notifier/notifier.go`
