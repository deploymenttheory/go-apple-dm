# Native webhooks

The reference server sends subscribed occurrences to HTTPS receivers. An occurrence
can describe a server outcome or a device-facing HTTP exchange. Webhooks belong to
the server module; the Apple protocol library's types, events and hooks are unchanged.

## Review the JSON contract

These examples are captured and POSTed to a test HTTPS receiver by
the [receiver example tests](../../server/webhook/examples_test.go). Each test verifies the
signature and compares the received JSON with the fixture. Random IDs are replaced
with readable example IDs; the fixture clock fixes timestamps and durations.

| Example | Receiver body |
|---|---|
| Command acknowledgment, summary | [JSON](../../server/webhook/testdata/mdm-command-summary.json) |
| Command acknowledgment, full decoded JSON | [JSON](../../server/webhook/testdata/mdm-command-full-json.json) |
| Command acknowledgment, original request and reply bytes | [JSON](../../server/webhook/testdata/mdm-command-raw.json) |
| Rejected identity, with an unverified claim | [JSON](../../server/webhook/testdata/mdm-rejected.json) |
| Malformed request | [JSON](../../server/webhook/testdata/mdm-malformed.json) |
| Idle polling with an empty reply | [JSON](../../server/webhook/testdata/mdm-idle.json) |
| TokenUpdate, including sensitive representations | [JSON](../../server/webhook/testdata/mdm-token-update.json) |
| DDM status, including the embedded JSON report | [JSON](../../server/webhook/testdata/ddm-status.json) |
| Enrollment profile reply | [JSON](../../server/webhook/testdata/enrollment-profile.json) |
| Service discovery | [JSON](../../server/webhook/testdata/enrollment-discovery.json) |
| SCEP rejection | [JSON](../../server/webhook/testdata/scep-rejected.json) |
| ACME error | [JSON](../../server/webhook/testdata/acme-error.json) |
| Certificate-status bytes | [JSON](../../server/webhook/testdata/certificate-status.json) |
| Content-cache report | [JSON](../../server/webhook/testdata/content-cache.json) |
| Bodies replaced by authenticated references | [JSON](../../server/webhook/testdata/large-reference.json) |
| Server command result, summary | [JSON](../../server/webhook/testdata/server-command-result.json) |
| Server command result, full decoded JSON | [JSON](../../server/webhook/testdata/server-command-result-full-json.json) |
| Replayed command result with previously uncaptured JSON | [JSON](../../server/webhook/testdata/server-command-result-replay.json) |
| Worker state | [JSON](../../server/webhook/testdata/server-worker-state.json) |
| Managed certificate lifecycle | [JSON](../../server/webhook/testdata/server-certificate-lifecycle.json) |

The fixtures use synthetic device data and controlled protocol handlers; the SCEP,
ACME and certificate-status bytes illustrate the envelope rather than valid issuance
transactions. [JSON Schema](../../server/webhook/event.schema.json) defines the
envelope. `dmctl webhooks catalogue` describes the current event vocabulary.
The [simulator integration test](../../server/internal/app/webhooks_test.go) also
exercises enrollment, SCEP, MDM commands and signed delivery through the application.

Each body has `schema_version`, `event_id`, `type`, `occurred_at`, `source` and `data`.
`subject` identifies a resource verified by the server. Unverified device claims
appear as `data.claimed_id`; they cannot match subject/channel filters. `source` is
the serving role (`all`, `mdm` or `ddm`). A `correlation_id` connects occurrences in
one incoming request and survives authenticated private DDM forwarding. It does not
imply delivery order or tie every later device response to its enqueue request.

For exchanges, `data.outcome` describes server processing independently of the
device's `data.device_status`. It is `succeeded`, `rejected`, `failed`, `incomplete`
or `unknown`. Protocol handlers that have no independent service-result annotation
use `unknown` for non-error HTTP replies. `data.http` records the HTTP method,
registered route, final status, duration and response completion. No query strings,
arbitrary headers, authorization values or cookies are included in the summary.
`command_type` is present when the server can associate a response with a retained
command; unknown or cleared commands can omit it.

Server outcomes use reviewed `data.actor` and `data.fields` projections. Examples
include `server.command.result`, `server.token.updated`, `server.ddm.status.received`,
`server.admin.action`, `server.dep.device.added`, `server.worker.state` and
`server.certificate.lifecycle`. The catalogue is explicit: a new internal event
does not automatically become an exportable native type.

## Enable and subscribe

Set `DM_WEBHOOKS_ENABLED=true` with a SQL backend, configured storage encryption
keys and administrator authentication. Subscription changes use the ordinary admin
API and `dmctl` connection configuration. No subscription means no native payload
retention. Paused subscriptions continue capturing; disabled subscriptions do not.

```json
{
  "name": "device-workflows",
  "url": "https://automation.example.test/mdm-events",
  "events": ["protocol.mdm.exchange", "protocol.ddm.exchange", "server.command.*"],
  "filters": {},
  "payload": {
    "full_json": false,
    "raw_request": false,
    "raw_response": false
  }
}
```

Save this as `subscription.json`, then:

```sh
dmctl webhooks create --file subscription.json
dmctl webhooks list
dmctl webhooks get --id SUBSCRIPTION_ID
dmctl webhooks test --id SUBSCRIPTION_ID
dmctl webhooks deliveries --subscription SUBSCRIPTION_ID
dmctl webhooks status
```

Creation returns a `subscription` and one-time `credentials`: a `whsec_` signing
secret and a separate `payload_token`. Normal reads never return credentials.
Store them at the receiver. Summary subscriptions can be delegated through Cedar's
`manageWebhooks` action, which grants export authority over server summaries.
`readWebhooks`, `readWebhookDeliveries` and `replayWebhooks` control the other actions.

Any payload option set to `true` makes the subscription sensitive. Creation, edits,
state changes, credential rotation, synthetic tests, retry and replay then require
an actual root principal in addition to route authorization. Granting a Cedar action
cannot confer that root authority. Full JSON is sensitive too: converting a plist
to JSON does not remove escrow, enrollment or push credentials.

Event selectors accept exact types, a known family followed by `.*`, or `*`.
Filters support `operations`, `outcomes`, `channels`, `subjects` and `command_types`.
Values within one field are ORed; different fields are ANDed. Missing fields do
not match a nonempty filter. Operation and outcome filters primarily select
protocol exchanges; command-type filters also select annotated command outcomes.

## Payload representations

| Policy | Parts |
|---|---|
| Summary only | No `payloads` member |
| `full_json` | `request_json`, `response_json`, embedded DDM `report_json` when present, or internal outcome `event_json` |
| `raw_request` | `request_raw`, base64 encoding the original application bytes consumed by the HTTP handler, before CMS decoding |
| `raw_response` | `response_raw`, base64 encoding bytes successfully written by the handler |

Decoded JSON preserves plist dictionary names; plist data becomes base64. The JSON
representation is compacted before measuring and hashing. Each available part has
its own byte `size` and hexadecimal SHA-256 digest. For JSON these describe that
compact representation; for raw data they describe the decoded bytes.

Availability is explicit: `complete`, `empty` (no decoded document), `undecodable`,
`not_read`, `incomplete`, `too_large`, or `not_captured` (replay). Unavailable parts
have no value or retrieval URL. Rejected bodies are never drained solely to export
them. HTTP transport framing, TLS records and headers are outside the byte contract.

When the complete serialized event would exceed 256 KiB, its body parts are replaced
with `href` and `expires_at`. Resolve each relative href against the operator's
configured public server origin and fetch with:

```http
GET /webhooks/v1/payloads/DELIVERY_ID/request_raw HTTP/1.1
Authorization: Bearer RECEIVER_PAYLOAD_TOKEN
```

The reply is the original bytes or compact JSON, with no base64 wrapper. Verify its
size and digest. The token is scoped to one subscription and its current revision;
it is not an administrator credential. Another subscription, an old revision, a
disabled/deleted destination or a rotated token cannot retrieve this body. Pausing
delivery preserves payload access. Expired bodies return 410 to an authenticated
receiver. Payload access does not itself produce webhook events.

## Delivery, rotation and replay

Every POST includes Standard Webhooks `webhook-id`, `webhook-timestamp` and
`webhook-signature`. HMAC-SHA256 signs `id.timestamp.body` using the decoded 32-byte
secret. Verify the exact incoming bytes before parsing, enforce a five-minute
timestamp window, and durably deduplicate `webhook-id`. The body `event_id` identifies
an occurrence; the header identifies a delivery. Automatic retries keep both IDs
and the body unchanged. Explicit replay keeps the occurrence ID and creates a new
delivery ID. Receivers choose whether replay should re-run their workflow.

`dmctl webhooks rotate --id ID` rotates signing and payload credentials. Signing uses
both keys for a default 24-hour overlap; `--overlap 0s` removes overlap. Payload-token
rotation is immediate. Updating a subscription requires its expected revision:

```sh
dmctl webhooks update --id ID --revision 1 --file subscription.json
dmctl webhooks pause --id ID
dmctl webhooks resume --id ID
dmctl webhooks disable --id ID
dmctl webhooks enable --id ID
dmctl webhooks delete --id ID
```

An update creates a revision and fresh credentials. Old pending/blocked deliveries
remain paused and cannot be resumed to the new URL or disclosure policy. Deletion
cancels outstanding deliveries. A request already in flight may still reach the
previous receiver when an administrator changes state or credentials.

HTTP 2xx acknowledges delivery. Timeouts, transport errors and HTTP 408/429/5xx retry
with exponential backoff, positive jitter and bounded Retry-After (maximum 24 hours).
Other HTTP responses block that delivery for operator action. Redirects are refused.
Worker leases support multiple replicas and restart recovery. No global or per-device
ordering is promised. Database/decryption failures stop the delivery worker instead
of being classified as transient receiver failures.

```sh
dmctl webhooks retry --id DELIVERY_ID
dmctl webhooks replay --file replay.json --dry-run
dmctl webhooks replay --file replay.json --key OPERATOR_REQUEST_ID
```

```json
{
  "subscription_id": "SUBSCRIPTION_ID",
  "event_ids": ["OCCURRENCE_ID"],
  "key": "OPERATOR_REQUEST_ID",
  "dry_run": false,
  "limit": 100
}
```

Replay also accepts exact `type`, inclusive `after` and exclusive `before` timestamps.
It applies the destination's current filters and disclosure ceiling. Preview reports
selected event IDs, missing representations and truncation without scheduling work.
Replay uses one retained snapshot per occurrence, preferring the snapshot with the
most complete or empty requested parts, then the most observed representations.
`not_captured` descriptors from previous replays do not count as captured data and
remain listed in `missing_payloads` on subsequent previews and replays.
Missing parts remain `not_captured`; there is no live-state
reconstruction or synthesis of uncaptured sensitive data. Non-root replay only reads
summary captures. A preview/operation selects at most 1,000 events and scans at most
10,000 retained snapshots; narrow the selection when `truncated` is true.

A successful non-preview request is idempotent under its key. Reusing that key with
different request parameters returns 409. Replaying does not extend the original
payload expiry. The CLI generates a key if none is supplied; supply and preserve
one explicitly when retrying an uncertain administrative request.

## Configuration and operational boundaries

| Variable | Default and effect |
|---|---|
| `DM_WEBHOOKS_ENABLED` | `false`; enable managed capture, API and workers |
| `DM_WEBHOOK_PAYLOAD_RETENTION` | `168h`; encrypted retained body lifetime |
| `DM_WEBHOOK_METADATA_RETENTION` | `720h`; delivery diagnostics lifetime, at least payload retention |
| `DM_WEBHOOK_MAX_BODY_BYTES` | `16777216`; bound each observed request/reply, permitted range 1 KiB–64 MiB |
| `DM_WEBHOOK_ROOT_CA_FILE` | Optional PEM roots added to system trust; hostname verification remains enabled |
| `DM_WEBHOOK_PRIVATE_NETWORKS` | Empty; comma-separated CIDRs explicitly permitting trusted private receiver networks |

Receiver URLs require HTTPS and reject userinfo and fragments. The transport
disables proxies and redirects, resolves DNS at connection time, checks every
returned address, then dials a checked address. Private, loopback and link-local
destinations need an explicit operator CIDR. Administrative users cannot alter this
process-wide outbound policy through subscriptions. Transport diagnostics contain
fixed local codes, not receiver response bodies, URLs or unwrapped errors.

Subscription configurations (including URLs and recoverable signing keys) and
retained payloads are encrypted with the configured storage keyring. Payload expiry
removes encrypted bodies and references; metadata expires separately. Backup schema
and key validation include the webhook tables. `webhook.Store.Rewrap` rotates both
sealed columns. Summary metadata and existing event/audit projections remain plaintext.

Participating local SQL mutations and their server-outcome capture commit together.
Exchange observations are recorded after the handler returns; failed observation
storage does not alter the reply. `GET /webhooks/status` exposes a process-local
capture-failure counter, queue counts and oldest retained occurrence per state.
Observe this counter on every serving replica. Worker status is available through
the existing event status/readiness surfaces. This is bounded retention with
observable capture gaps, not an unlimited lossless protocol journal.

Native delivery bookkeeping, payload fetches and per-attempt failures do not create
recursive webhook events. Administrative operations may create ordinary admin-action
outcomes. Health endpoints and authenticated internal DDM exchanges are excluded
from the public exchange feed; DDM service outcomes retain the forwarded correlation.

## HTTP administration

All routes below are prefixed by `/admin/v1`. Collection pages use `after` and `limit`
(default 100, maximum 1,000); cursors are IDs, not timestamps.

| Method and path | Body / purpose |
|---|---|
| `GET /webhooks`, `GET /webhooks/{id}` | Subscription configuration without credentials |
| `POST /webhooks` | Subscription specification; returns one-time credentials |
| `PUT /webhooks/{id}` | `{"revision":1,"spec":{...}}` |
| `DELETE /webhooks/{id}` | Disable and cancel outstanding deliveries |
| `POST /webhooks/{id}/{pause,resume,disable,enable}` | State transition |
| `POST /webhooks/{id}/credentials` | `{}` or `{"overlap_seconds":0}` |
| `POST /webhooks/{id}/test` | Queue a synthetic `webhook.test` event |
| `GET /webhooks/catalogue`, `GET /webhooks/status` | Vocabulary and operational state |
| `GET /webhooks/deliveries` | Optional `subscription_id`, safe metadata only |
| `GET /webhooks/deliveries/{id}` | One delivery's safe metadata |
| `POST /webhooks/deliveries/{id}/retry` | Retry an eligible current-revision delivery |
| `POST /webhooks/replays` | Preview or idempotent replay selection |

## Migration

`DM_WEBHOOK_URL` and `DM_WEBHOOK_HMAC_KEY` now fail startup with explicit migration
guidance. Replace them with managed subscriptions and install the new receiver
credentials. Legacy event/delivery history stays in the existing tables; native
subscriptions never silently inherit or reroute it. Legacy projected history has no
native retained bodies and cannot be reconstructed by native replay. The native
envelope is independent of MicroMDM compatibility.

References: [decision 0037](../research/decisions/0037-event-sinks-and-redaction.md),
[event delivery](event-delivery.md), [receiver example](../../server/examples/webhook-receiver/README.md),
[Standard Webhooks specification](https://github.com/standard-webhooks/standard-webhooks/blob/main/spec/standard-webhooks.md).
