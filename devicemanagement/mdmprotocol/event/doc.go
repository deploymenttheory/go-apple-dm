// Package event provides an in-process bus for typed events with enrollment,
// actor and timestamp metadata.
//
// # Design
//
// Subscribers can observe lifecycle and command outcomes without being coupled
// to the service implementation. The bus supports synchronous and asynchronous
// dispatch and configurable error reporting. WithAsync uses eight workers, a
// 1,024-event pending queue and a 30-second lifetime from acceptance. NewAsync
// accepts explicit limits. Publish rejects excess events with ErrQueueFull;
// rejection is counted and reported even if the caller ignores the error.
// Stats exposes backlog and cumulative outcomes without event contents.
//
// Accepted events retain context values and survive request cancellation, but
// expire on the bus-owned deadline. Subscribers run in registration order for
// each event; concurrent events have no delivery-order guarantee. Handlers and
// error callbacks must return promptly and handlers must honor cancellation.
// A non-cooperating handler occupies a worker; it does not trigger another one.
//
// Always Close an asynchronous bus. Close stops acceptance and drains within
// its context deadline, then cancels active contexts and abandons queued events.
// Subsequent Close calls may wait for handlers that have not returned. The bus
// has no durable storage or replay; audit and webhook delivery can have gaps
// after overload, expiry, sink failure or abrupt termination. Queue limits bound
// event counts rather than payload byte sizes; producers must bound payloads.
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
