// Package inmem is the reference dep.Store: a mutex-protected map store
// whose behaviour the contract suite in dep/deptest defines.
//
// # Why
//
// The DEP client, syncer, assigner, and the fake service's tests all need
// a dep.Store that is always compiled and needs no database, and the
// contract suite needs a backend simple enough to be obviously right.
// Phase 6 of the plan of record delivers this one with the client
// (decision record 0026). Update copies the state, runs the callback
// against the copy, and swaps the copy in only when the callback
// succeeds, which gives the transactional semantics the suite demands (a
// page committed with its cursor, an atomic keypair upstage) without a
// transaction log. With WithKeyring the OAuth secrets, session tokens,
// and private keys are sealed in memory through server/storage/crypt exactly as
// dep/sqlstore seals its columns, so the sealing contract is one code
// path on both backends.
//
// It is not persistent. Deployments use dep/sqlstore, which passes the same
// suite.
//
// # References
//
//   - Decision record 0026: docs/research/decisions/0026-dep-client-sync-and-assignment.md
//   - Decision record 0013: docs/research/decisions/0013-secrets-at-rest.md
//   - Plan of record: docs/research/implementation_plan.md (section 5, DEP / ABM; phase 6)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/device-assignment
//   - Apple: https://developer.apple.com/documentation/devicemanagement/device
package inmem
