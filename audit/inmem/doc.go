// Package inmem is the in-memory audit trail: the backend every unit test
// uses and the one a deployment without a database falls back to.
//
// # Why
//
// The contract in package audit has to be satisfiable without a database, so
// the reference server behaves the same way whether or not one is configured
// and so the suites that exercise the trail need no fixture. It is unsealed
// and unbounded on purpose: records live as long as the process, and a
// deployment that needs them to outlive it configures audit/sqlstore.
//
// # References
//
//   - Decision record 0038: docs/research/decisions/0038-persisted-audit-trail.md
//   - Contract suite: audit/audittest
//   - Plan of record: docs/research/implementation_plan.md (phase 9)
package inmem
