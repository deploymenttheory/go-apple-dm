// Package event is the in-process event bus every state change in the
// service layer publishes to: typed events with an enrollment id, an actor,
// and a timestamp, dispatched to subscribers by type.
//
// # Why
//
// Webhooks, audit logs, metrics, and the DDM change notifier all want to
// know when an enrollment appears, a token changes, a command completes, or
// a certificate rotates. Making them subscribers of one bus rather than
// special cases inside the service is the shape decision record 0001 chose
// for the library, and the threat model's repudiation control depends on
// it: every state change emits an event, and subscribers persist them.
// Phase 2 of the plan of record delivers the bus alongside the protocol
// core.
//
// The bus is deliberately small: synchronous dispatch, a handler error
// reported through the bus error handler without stopping other handlers,
// and no persistence. The sinks live in event/sink, which projects an event
// down to what may leave the process before an slog record or a webhook
// carries it; the persistent trail the threat model's repudiation control needs
// is package audit.
//
// # References
//
//   - Decision record 0001: docs/research/decisions/0001-architecture.md
//   - Decision record 0006: docs/research/decisions/0006-mdm-signature-verification.md (CertRotated)
//   - Decision record 0015: docs/research/decisions/0015-push-cert-store.md
//   - Plan of record: docs/research/implementation_plan.md (phase 2)
//   - Threat model: docs/security/threat-model.md (Repudiation)
package event
