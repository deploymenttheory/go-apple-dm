// Package inmem provides an in-memory audit trail for tests and development.
//
// # Design
//
// The backend follows the same ordering, filtering and pruning contract as SQL
// stores. Records are unsealed and remain until pruned or the process exits; no
// automatic size cap is imposed. Consumers requiring persistence or bounded
// retention must configure those controls explicitly.
//
// # References
//
//   - Decision record 0038: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0038-persisted-audit-trail.md
//   - Contract suite: audit/audittest
package inmem
