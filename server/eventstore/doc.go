// Package eventstore captures projected events in persistent SQL state and
// delivers each configured destination independently with leases and retries.
// Webhook delivery is at least once; consumers deduplicate using EventID.
// Native destinations using the same SQL pool can atomically append and
// acknowledge delivery through Worker.TransactionalDestinations.
package eventstore
