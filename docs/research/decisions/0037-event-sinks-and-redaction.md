# 0037: Event sinks and default-deny redaction

## Context

Events can contain protocol messages with escrowed secrets and private device data. External logs and webhooks need explicit field selection.

## Decision

A projection registry selects fields for each event type. Unknown types emit metadata only. The slog sink, webhook and persisted audit trail consume projected records. Webhooks use the MicroMDM-compatible envelope while omitting `raw_payload`.

Webhook delivery runs through the reference server's asynchronous event bus, with bounded response reads and retries. Construction requires HTTPS and rejects URL credentials and fragments. Redirects are refused. `DM_WEBHOOK_ROOT_CA_FILE` supplies a private CA bundle without disabling hostname verification. Transport error strings omit the configured URL, including sensitive path/query values; trusted callers can still inspect wrapped causes. Closing the application drains the bus.

## Rationale

An allowlist keeps new fields from leaving the process unless they are reviewed. A common projection gives every sink the same disclosure policy. Asynchronous delivery separates receiver latency from device request handling.

## Constraints

The bus queue and webhook retries are not durable across process failure. An audit record does not automatically replay a failed webhook. Direct subscribers to the internal bus can receive unprojected data and must enforce their own disclosure policy. MicroMDM receivers requiring `raw_payload` are not byte-for-byte compatible.

## Verification

Projection tests seed payloads with sentinel secrets, cover every event type and reject mismatched payload types. Sink tests cover the envelope, retries, cancellation, failed destinations and redaction; application tests cover event delivery and close/drain behavior.

## References

- [server/eventsink](../../../server/eventsink)
- [mdmprotocol/event](../../../mdmprotocol/event)
- [server/internal/app/app.go](../../../server/internal/app/app.go)
- <https://developer.apple.com/documentation/devicemanagement/check-in>
- <https://developer.apple.com/documentation/devicemanagement/tokenupdate>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10`, `service/webhook/service.go`
- `service/webhook/event.go`, `service/webhook/event.json`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107`, `workflow/webhook/webhook.go`
- `workflow/webhook/checkin.go`, `workflow/webhook/acknowledge.go`, `workflow/webhook/http_post.go`
- `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151`, `notifier/notifier.go`
