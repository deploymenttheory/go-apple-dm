// Package sink publishes events off the bus: a projection registry that
// decides what each event type may say, an slog audit sink, and a
// MicroMDM-compatible webhook.
//
// # Why
//
// The bus has had no subscribers outside tests since it was built. Every
// state change was announced to an empty room, which left two commitments
// unmet: event/doc.go promised webhook and OpenTelemetry sinks with the
// reference server, and the threat model's repudiation control asserts that
// "every state change emits an event, and subscribers persist them". Phase 9
// of the plan of record makes both true, and this package is the half that
// decides what may leave the process. The persisted half is package audit.
//
// The design is default-deny projection rather than redaction. A sink that
// marshals Event.Data reflectively publishes whatever a publisher happened to
// attach, and one of the payloads is a *checkin.TokenUpdate, which carries
// UnlockToken -- the secret that clears a device passcode -- beside the push
// token, PushMagic, and the user's short and long names. NanoMDM and MicroMDM
// both base64 the whole check-in body into raw_payload and send it, so all of
// that reaches the receiver. Here an event type publishes only the fields its
// registered projection names, and a type with no projection publishes
// nothing but metadata. Forgetting a new event yields a thinner record, never
// a leaked secret, and the difference between "considered and bare" and
// "forgotten" is kept by Registry.Known so a test can insist on it.
//
// The webhook keeps MicroMDM's envelope so existing receivers work, and drops
// exactly one field, raw_payload, for the reason above. It is deliberately not
// configurable back on.
//
// # References
//
//   - Decision record 0037: docs/research/decisions/0037-event-sinks-and-redaction.md
//   - Decision record 0001: docs/research/decisions/0001-architecture.md (the bus)
//   - Plan of record: docs/research/implementation_plan.md (phase 9)
//   - Threat model: docs/security/threat-model.md (repudiation)
//   - micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10 service/webhook/service.go, event.go
//   - micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107 workflow/webhook/webhook.go, checkin.go, http_post.go
//   - Apple documents no webhook or audit surface; the protocol events these
//     records describe are the check-in and command pages cited by ddm and
//     service.
//   - RFC 2104: HMAC, used to sign the webhook body
package sink
