// Package audittest defines audit-store contracts and controlled failure
// injection.
//
// # Design
//
// RunSuite covers ordering, filtering, cursor pagination, the exclusive age
// boundary for pruning and non-reused IDs. Every backend runs the same cases so
// retention and queries have consistent semantics. Failing lets handlers and
// workers exercise database-error behavior with deterministic tests.
//
// # References
//
//   - Decision record 0038: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0038-persisted-audit-trail.md
//   - Contract: audit.Store
package audittest
