// Package acmetest defines ACME store contracts, fixtures and controlled
// failures.
//
// # Design
//
// RunAll checks account identity, one-time identifiers/nonces, atomic order
// creation, pagination, pruning and exact attestation-byte round trips. Those
// bytes are re-verified against the CSR at finalize. Backends choose their own
// opaque cursor encoding. Failing wraps transactions as well as store methods so
// handler error paths can be exercised without damaging a database.
//
// # References
//
//   - Decision record 0031: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0031-acme-server-and-state-store.md
//   - Decision record 0032: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0032-managed-device-attestation.md
//   - RFC 8555 (ACME): https://www.rfc-editor.org/rfc/rfc8555
//   - Apple: https://support.apple.com/en-gb/guide/deployment/dep28afbde6a/web
package acmetest
