// Package inmem implements a mutex-protected in-memory acme.Store.
//
// # Design
//
// Update preserves the original maps for rollback when a callback fails. Reads
// and writes deep-copy stored values, including attestation bytes, so callers
// cannot mutate issuance state through returned slices. The acmetest suite
// defines atomicity, nonce consumption and identifier-claim behavior.
//
// All accounts, orders, certificates and nonces are lost on restart. Durable
// deployments use server/acmestore/sqlstore or another implementation of the
// contract.
//
// # References
//
//   - Decision record 0031: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0031-acme-server-and-state-store.md
//   - Decision record 0032: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0032-managed-device-attestation.md
//   - RFC 8555 (ACME): https://www.rfc-editor.org/rfc/rfc8555
//   - Apple: https://support.apple.com/en-gb/guide/deployment/dep28afbde6a/web
package inmem
