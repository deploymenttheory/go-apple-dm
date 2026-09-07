// Package state defines atomic, expiring byte records for protocol state.
//
// # Why
//
// Token exchanges, enrollment associations, revocation and quota accounting need
// transactions without depending on a database driver. Memory is suitable for a
// single process; server/statestore supplies shared SQL persistence. Callers own
// serialization, key namespaces and the lifetime of their records.
//
// # References
//
//   - docs/research/decisions/0047-enrollment-authentication-and-optional-security-services.md
//   - docs/security/threat-model.md
package state
