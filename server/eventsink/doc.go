// Package eventsink projects event fields for slog, webhooks and persisted audit
// records.
//
// # Design
//
// Registry permits only explicitly selected fields for each event type; unknown
// types emit metadata only. Known distinguishes an intentionally metadata-only
// projection from an unreviewed type. This avoids exporting raw protocol
// structures containing escrowed tokens or other sensitive fields.
//
// The webhook uses a MicroMDM-compatible envelope without raw_payload. It
// supports bounded replies, retries and optional body HMAC signing. The
// reference application runs delivery through the asynchronous bus; the queue is
// not durable across process failure. Direct bus subscribers must apply their
// own disclosure policy.
//
// # References
//
//   - Decision record 0037: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0037-event-sinks-and-redaction.md
//   - Decision record 0001: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0001-architecture.md (the bus)
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (repudiation)
//   - micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10 service/webhook/service.go, event.go
//   - micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107 workflow/webhook/webhook.go, checkin.go, http_post.go
//   - Apple documents no webhook or audit surface; the protocol events these records describe are the check-in and command pages cited by ddm and service.
//   - RFC 2104: HMAC, used to sign the webhook body
package eventsink
