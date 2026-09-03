// Package inmem is an in-memory adminauth.Store for tests and for the
// reference server's development mode.
//
// # Why
//
// Every adminauth backend runs the same contract suite, and the in-memory one
// is what the unit tests use so they need no database. It is also what the
// reference server falls back to when it is configured with in-memory storage,
// where principals and policies live only as long as the process.
//
// Nothing here is sealed at rest: process memory is inside the trust boundary,
// and the store holds token digests rather than tokens, so there is no
// plaintext credential to protect. The same reasoning kept storage/inmem
// unencrypted in record 0013.
//
// # References
//
//   - Decision record: docs/research/decisions/0034-admin-api-and-authorization.md
//   - Plan of record: docs/research/implementation_plan.md (phase 8)
//   - Contract suite: adminauth/adminauthtest
package inmem
