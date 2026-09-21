// Package eventstore captures projected events in persistent SQL state and
// delivers each configured destination independently with leases and retries.
//
// # Design
//
// Delivery is at least once. Projected-record receivers deduplicate using the
// record's EventID. Native webhook deliveries use their own delivery_id; replay
// creates a new delivery for the same event_id. The native adapter stores that
// delivery ID in the outbox record's EventID field.
// Native destinations using the same SQL pool can atomically append and
// acknowledge delivery through Worker.TransactionalDestinations.
//
// # References
//
//   - Event delivery: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/architecture.md
//   - Native delivery adapter: https://github.com/deploymenttheory/go-apple-dm/blob/main/server/webhook/capture.go
//   - Audit design: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0038-persisted-audit-trail.md
package eventstore
