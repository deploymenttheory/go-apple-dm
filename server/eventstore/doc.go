// Package eventstore captures projected events in persistent SQL state and
// delivers each configured destination independently with leases and retries.
//
// # Design
//
// Webhook delivery is at least once; consumers deduplicate using EventID.
// Native destinations using the same SQL pool can atomically append and
// acknowledge delivery through Worker.TransactionalDestinations.
//
// # References
//
//   - Event delivery: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/architecture.md
//   - Audit design: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0038-persisted-audit-trail.md
package eventstore
