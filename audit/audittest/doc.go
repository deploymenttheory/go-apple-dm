// Package audittest is the contract every audit backend must satisfy, and a
// store that fails on demand.
//
// # Why
//
// The in-memory and SQL trails are read by the same admin route and pruned by
// the same worker, so a difference between them is a bug an operator finds
// while investigating an incident, which is the worst moment. RunSuite is the
// single definition of the behaviour: ordering, filtering, cursor pagination
// across filters, the exclusive prune boundary, and that ids are never reused
// after a prune so a stale cursor cannot replay the wrong records.
//
// Failing exists because a handler's error path is otherwise unreachable
// without breaking a real database.
//
// # References
//
//   - Decision record 0038: docs/research/decisions/0038-persisted-audit-trail.md
//   - Contract: audit.Store
//   - Plan of record: docs/research/implementation_plan.md (phase 9)
package audittest
