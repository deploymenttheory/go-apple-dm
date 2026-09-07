// Package event provides an in-process bus for typed events with enrollment,
// actor and timestamp metadata.
//
// # Design
//
// Subscribers can observe lifecycle and command outcomes without being coupled
// to the service implementation. The bus supports synchronous and asynchronous
// dispatch and configurable handler-error reporting. Close drains queued
// asynchronous work. The bus itself has no durable storage.
//
// Events may contain sensitive protocol data. External sinks in server/eventsink
// apply an explicit projection, and server/audit persists that projection when
// configured. Direct subscribers must apply their own disclosure policy. The DDM
// notifier consumes persistent change rows rather than relying on bus delivery.
//
// # References
//
//   - Decision record 0001: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0001-architecture.md
//   - Decision record 0006: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0006-mdm-signature-verification.md (CertRotated)
//   - Decision record 0015: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0015-push-cert-store.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (Repudiation)
package event
