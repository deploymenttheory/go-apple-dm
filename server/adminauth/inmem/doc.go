// Package inmem implements an in-memory adminauth.Store for tests and
// development.
//
// # Design
//
// Principals, token digests and policies follow the shared adminauthtest
// contract. Data lasts only for the process lifetime and is not sealed. The
// reference server selects this backend when configured without persistent
// storage; deployments requiring durable credentials and policy use
// server/adminauth/sqlstore.
//
// # References
//
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0034-admin-api-and-authorization.md
//   - Contract suite: adminauth/adminauthtest
package inmem
