// Package maintenance coordinates a pause in server writes before a checkpoint.
//
// # Design
//
// Processes register participants in shared SQL storage. When an operator
// requests a pause, participants stop accepting requests, cancel their workers
// and wait for existing writes to finish before acknowledging. The worker
// callback must honor cancellation and return only after its writes stop.
// WaitDrained succeeds only when every registered participant has acknowledged.
//
// Participants never expire automatically: a missed heartbeat does not prove
// that a process stopped writing. Operators must stop an unreachable process
// before forgetting it. Cancellation leaves the persistent pause in place;
// resuming writes requires the matching ownership ticket.
//
// # References
//
//   - https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/recovery.md
package maintenance
