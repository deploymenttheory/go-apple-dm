# 0037: Event sinks and default-deny redaction

## Context

Protocol messages can contain escrowed secrets, enrollment credentials and private
device data. External workflows need stable server outcomes and a device exchange
feed, while audit readers need reviewed summaries.

## Decision

The projection registry in `server/eventsink` selects fields for slog,
the persistent event journal and audit. Unknown internal types emit metadata only;
registering a nil projection explicitly selects metadata only.

Native webhooks live in `server/webhook`. The root Apple protocol library gains no
webhook envelope, delivery transport, subscriptions or storage interfaces. The server
adapts existing typed events, observes device-facing HTTP exchanges before CMS decoding
and after response writes, and wraps managed certificate state transitions locally.
The [webhook guide](../../operations/webhooks.md) defines the versioned envelope,
explicit catalogue, payload policy, administration and receiver examples.

Managed subscriptions require SQL and storage encryption. Only enabled, matching
subscriptions retain native captures. Summary delivery uses reviewed projections;
full decoded JSON and original bodies require explicit sensitive-operation grants
in addition to the ordinary route grant. `manageSensitiveWebhooks` controls
destination operations and `replaySensitiveWebhooks` controls sensitive retry/replay.
Root receives no implicit fleet grants. Subscription URLs and keys are encrypted.
Receiver payload credentials are separate from administrator credentials and scoped
to the current subscription revision.

Participating local SQL mutations and server-outcome capture commit together through
`event.Run`. Capture failure rolls back the local operation. Public exchange capture
runs after the protocol handler and records observation failure without changing the
response. Unverified claims are distinct from verified subjects. Correlation groups observations from one request; the reference-server source is
`device-management`. Delivery order is unspecified.

Encrypted retained messages use the existing outbox's leases and retry worker. Outbox
markers contain only scheduling metadata. HTTPS transport verifies trust and hostname,
checks resolved addresses at dial time, requires explicit private-network CIDRs,
refuses redirects and bounds receiver replies. Standard Webhooks signatures authenticate
the delivery ID, attempt timestamp and immutable body. Key rotation permits bounded
overlap. Payload bodies over the inline bound become authenticated references.

A destination update creates a revision and pauses old backlog. Replay is explicit,
bounded and idempotent, creates a new delivery ID for the same occurrence, and only
uses retained representations under the current disclosure ceiling. It cannot extend
expiry or reconstruct data from current device state. Retention defaults to seven
days for bodies and thirty days for delivery metadata. Credential/payload access and
per-attempt bookkeeping do not recursively produce webhook occurrences.

In-memory applications can still use ephemeral slog,
audit and direct bus subscribers; managed webhooks require persistent encrypted SQL.

## Rationale

Audit projections and sensitive workflow export have different disclosure contracts.
Separately authorized sensitive destinations preserve that distinction across configuration,
credentials and replay. A native envelope covers polling, failed exchanges and
non-MDM server outcomes without constraining the protocol library to a workflow API.
Persisted capture and independent delivery avoid adding receiver latency to a device
request. Separate HTTP observations describe bytes actually consumed/written without
claiming an atomic transaction with a remote device.

## Constraints

Delivery is at least once, unordered and bounded by retention. Receivers deduplicate
`webhook-id`; explicit replay may need independent occurrence-level deduplication.
An already-started network request can finish after a destination is paused or edited.
A process crash or storage failure after the device reply can leave a missing exchange
observation. Per-process capture counters and worker readiness expose failures.

SQL coordination does not span external Apple calls or independently supplied stores.
Unconsumed, oversized, interrupted and undecodable payloads have explicit availability
markers. A successful HTTP reply alone does not prove a successful protocol operation.
Plaintext summary metadata, database administrators and external receivers remain
outside payload encryption's protection boundary. Direct bus subscribers receive
internal data and remain responsible for their own disclosure policy.

## Verification

Receiver fixtures compare actual delivered JSON and verify signatures. Tests cover
transaction rollback, sensitive permission gates, retained snapshot replay, revision and
credential isolation, expiration, restart, concurrent SQL leases, database failures,
protocol observations and unchanged replies. PostgreSQL, MySQL and SQLite exercise
the native store. Existing projection, audit, bus and service suites preserve their
separate contracts. The Python receiver verifies the same Standard Webhooks signing
input independently.

## References

- [Native webhook guide](../../operations/webhooks.md)
- [Reference-server authorization](../../../server/internal/app/webhooks.go)
- [Webhook implementation and contract](../../../server/webhook)
- [Persistent outbox](../../../server/eventstore)
- [Safe projections](../../../server/eventsink)
- [Library events](../../../devicemanagement/mdmprotocol/event)
- [Standard Webhooks specification](https://github.com/standard-webhooks/standard-webhooks/blob/main/spec/standard-webhooks.md)
