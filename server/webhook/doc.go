// Package webhook implements the reference server's native webhook contract,
// subscription management, encrypted capture and receiver authentication.
//
// Delivery uses eventstore's persistent leases. Payloads are immutable for a
// delivery; replay creates another delivery for the same occurrence. Network
// delivery is at least once and unordered. Consumers deduplicate delivery IDs.
// Full decoded bodies and original bytes require root-managed subscriptions.
package webhook
