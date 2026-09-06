# 0037: Event sinks and default-deny redaction

Status: accepted
Date: 2026-09-03
Phase: 9

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> (the `TokenUpdate`
  message whose payload forces this record's central decision)
- Doc: <https://developer.apple.com/documentation/devicemanagement/tokenupdate> (`UnlockToken`,
  `Token`, `PushMagic`: the three secrets a check-in carries)
- YAML: `third_party/device-management/mdm/checkin/tokenupdate.yaml` — `UnlockToken` is documented
  as "the data to use to unlock the device", retained by the server for `ClearPasscode`
- YAML: `third_party/device-management/mdm/checkin/authenticate.yaml` — the device description an
  enrollment record is worth keeping

Framing: Apple documents no webhook, audit or notification surface at all. Everything here is an
integration concern invented by MDM servers, so the prior art is the whole of the source material
and the Apple pages matter only for what the payloads contain.

Dependency note: none. The sinks use `net/http`, `log/slog` and `crypto/hmac` from the standard
library. The plan named OpenTelemetry adapters alongside webhooks in `event/doc.go`; metrics are a
separate record because they instrument call sites rather than subscribe to the bus.

## References read

- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10` `service/webhook/service.go`,
  `service/webhook/event.go`, `service/webhook/event.json`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107` `workflow/webhook/webhook.go`,
  `workflow/webhook/checkin.go`, `workflow/webhook/acknowledge.go`, `workflow/webhook/http_post.go`
- `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151` `notifier/notifier.go` (checked
  for a status webhook; it has none, so the DDM status feed has no prior art to follow)

## Known pitfalls found

- `nanomdm/service/webhook/service.go:150-165`: `TokenUpdate` sends `RawPayload: b64(m.Raw)` — the
  entire check-in plist, base64-encoded. That body contains `UnlockToken`, the secret that clears a
  device passcode, plus the push token, `PushMagic`, and the user's short and long names. Every one
  of those reaches the webhook receiver and every proxy, log and queue in front of it.
- `micromdm/workflow/webhook/checkin.go:16-33`: the same leak in the original. `RawPayload []byte`
  is `ev.Raw` verbatim. This is where the format came from, so any server claiming MicroMDM
  compatibility inherits it.
- `nanomdm/service/webhook/service.go:149`: delivery happens inside the check-in handler, so a slow
  or hanging receiver adds its latency to every device check-in. There is no queue and no timeout
  beyond the HTTP client's.
- `nanomdm/service/webhook/service.go:136-146` and `micromdm/workflow/webhook/http_post.go:26-38`:
  one attempt, no retry. A receiver that is briefly down loses the event permanently.
- `micromdm/workflow/webhook/http_post.go:22`: `json.MarshalIndent` for a machine-to-machine
  payload, and no bound on the response body either reads back.
- Ours: nothing has subscribed to `event.Bus` outside tests since it was built. `event/doc.go`
  promised sinks "with the reference server in phase 8" and named the wrong phase after the split,
  and the threat model has asserted "subscribers persist them" while no subscriber existed.

## What they do

- **NanoMDM**: a `Webhook` service implementing the check-in and connect interfaces, sending a
  JSON envelope generated from MicroMDM's schema — `topic`, `event_id`, `created_at`, and one of
  `checkin_event` or `acknowledge_event`. Optional SHA-256 HMAC of the body in `X-Hmac-Signature`.
  Optional token-update tally from storage. Payload is always the raw check-in body, base64. Sent
  synchronously from the handler; any non-200 is an error; one attempt.
- **MicroMDM**: a pubsub worker subscribing to `mdm.Connect`, `mdm.Authenticate`, `mdm.TokenUpdate`
  and `mdm.CheckOut`, posting the same envelope. `RawPayload` is the raw plist as bytes. No HMAC,
  no retry, no body bound. Being a worker rather than a handler, it is at least off the check-in
  path.
- **KMFDDM**: no webhook. The DDM status feed is only observable through its API.

## What we do better

1. **A projection registry that is default-deny, not a redaction blocklist.** An event type
   publishes only the fields its registered projection names. `TokenUpdate` yields `topic`,
   `not_on_console` and `awaiting_configuration`, and nothing else can escape it, where both
   references forward the whole message. A blocklist would be the wrong shape: it fails open when
   Apple adds a key, and Apple adds keys every September.
2. **Forgetting an event is safe.** A type with no projection publishes metadata only, so the
   failure mode of an oversight is a thinner audit record rather than a leaked secret.
   `Registry.Known` distinguishes "considered and deliberately bare" from "never considered", so a
   test can require every declared type to have been thought about.
3. **The leak is proven absent rather than asserted.** Every payload the module publishes is seeded
   with sentinel secrets and run through every projection, the slog sink and the webhook body.
4. **MicroMDM's envelope without MicroMDM's payload.** Receivers keep working; `raw_payload` is
   absent, deliberately and without a switch to restore it. The events carry parsed messages rather
   than the bytes, so offering the option would mean re-plumbing the raw body through the bus in
   order to make the leak configurable.
5. **Delivery is off the check-in path.** The reference server subscribes the webhook to an
   asynchronous bus, and `Close` drains it, so a hanging receiver cannot delay a device and a
   shutdown cannot silently drop a delivery in flight.
6. **Retry with backoff, and a bounded read of the receiver's reply**, where neither reference
   retries at all and neither bounds what it reads back.
7. **An unusable webhook URL fails at construction**, not once per event for the life of the
   process.

## Verified by

1. `sink.TestTokenUpdateProjectsOnlyOperationalFields` (proves claim 1; would fail on either
   reference, which put `UnlockToken` in `raw_payload`).
2. `sink.TestUnregisteredEventProjectsMetadataOnly`, `sink.TestMetadataOnlyIsDistinctFromUnknown`,
   `sink.TestEveryEventTypeIsProjected` (claim 2).
3. `sink.TestNoSecretSurvivesProjection`, `sink.TestSlogSinkWritesProjectedRecords`,
   `sink.TestWebhookNeverSendsSecrets`, `sink.TestEveryProjectionRefusesAMismatchedPayload`
   (claim 3).
4. `sink.TestWebhookSendsTheMicroMDMEnvelope` asserts the envelope fields and that `raw_payload` is
   absent; `sink.TestWebhookSplitsAcknowledgeFromCheckin` asserts the two-arm split (claim 4).
5. `app.TestCloseDrainsTheEventBus`, `app.TestWebhookSinkReceivesEnrollmentEvents` (claim 5).
6. `sink.TestWebhookRetriesThenSucceeds`, `sink.TestWebhookReportsAPersistentFailure`,
   `sink.TestWebhookStopsOnCancellation` (claim 6).
7. `sink.TestWebhookRejectsABadURL`, `app.TestBadWebhookURLFailsBuild` (claim 7).

## Rejected alternatives

- Marshalling `Event.Data` reflectively with a list of forbidden field names: fails open on every
  new Apple key, and needs to know the shape of every payload type anyway. The projection is the
  same amount of code and fails closed.
- A `Redact()` method on each payload type: puts sink policy in `schema/*`, which is generated, and
  in `mdm`, which should not know a webhook exists.
- Offering `IncludeRawPayload` for byte-level MicroMDM compatibility: the events carry parsed
  messages, so it would require threading raw bodies through the bus purely to make the leak
  available. A receiver that must parse the plist wants the check-in handler, not a webhook.
- Persisting from the sink directly: the persistent trail is a storage concern with its own contract
  suite and retention, and is record 0038.
- A dedicated delivery queue with its own worker: the bus's async mode already decouples delivery,
  and a queue that survives restart is what the audit table is for.
- Signing with an `Authorization` header instead of an HMAC: the HMAC header matches NanoMDM, so an
  existing receiver's verification code works unchanged.
