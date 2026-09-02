// Package inmem is the reference acme.Store: a mutex-protected map store
// whose behaviour the contract suite in acme/acmetest defines.
//
// # Why
//
// The ACME server, its handlers, and the simulator's enrollment tests all
// need an acme.Store that is always compiled and needs no database, and
// the contract suite needs a backend simple enough to be obviously right.
// Phase 7 of the plan of record delivers the ACME server with
// device-attest-01 (decision record 0031), and this is the store it is
// written against. Update takes a copy of every map on entry and puts the
// copies back when the callback fails, so an order, its authorization, its
// challenge, and the claim on its client identifier either all exist or
// none do, which is the property the server relies on to refuse a replayed
// identifier under concurrency. Every read returns a deep copy and every
// write stores one, including the attestation bytes: the server verifies
// the stored attestation again at finalize, against the key in a
// certificate request it had not seen when the challenge was answered, so
// a backend that let a caller reach into stored bytes would break
// issuance in a way no unit test of the caller would show.
//
// It is not durable, and nonces resting only in memory means a restart
// invalidates every outstanding one; a client answers a badNonce by
// retrying with a fresh nonce, so that is a stumble rather than a fault.
// Deployments use a persistent backend, which passes the same suite.
//
// # References
//
//   - Decision record 0031: docs/research/decisions/0031-acme-server-and-state-store.md
//   - Decision record 0032: docs/research/decisions/0032-managed-device-attestation.md
//   - Plan of record: docs/research/implementation_plan.md (section 9, phase 7)
//   - RFC 8555 (ACME): https://www.rfc-editor.org/rfc/rfc8555
//   - Apple: https://support.apple.com/en-gb/guide/deployment/dep28afbde6a/web
package inmem
