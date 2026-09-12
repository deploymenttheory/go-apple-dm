// Package state defines transactional, expiring byte records for protocol state.
//
// # Design
//
// Token exchange, certificate associations, revocation and quotas need atomic
// updates without importing database drivers. Memory supports a single process,
// while server/statestore provides shared SQL persistence. Transactions expose
// store time so expiry and quota decisions can use a consistent clock. Callers
// own serialization, namespaces, key selection and record lifetimes. In-memory
// state is lost on restart.
//
// # References
//
//   - https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0047-enrollment-authentication-and-optional-security-services.md
//   - https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md
package state
