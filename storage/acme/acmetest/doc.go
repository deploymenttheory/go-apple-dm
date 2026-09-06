// Package acmetest is the test bed for the ACME state store: the contract
// suite every acme.Store backend must satisfy, a Failing store that
// injects errors by method name, and sample records the server's own
// tests build on.
//
// # Why
//
// The ACME server keeps no state of its own, so every property a device
// depends on is a property of the store: an account is its key, a client
// identifier is claimed once, a nonce is taken once, an order and its
// authorization and challenge appear together or not at all, and the
// attestation bytes come back exactly as they arrived because finalize
// verifies them again against a certificate request the challenge never
// saw. Phase 7 of the plan of record delivers the server (decision record
// 0031) against acme/inmem, and a persistent backend has to be held to the
// same rule rather than to a reading of it, so the rule is written once
// here and every backend runs it.
//
// The suite says nothing about how a backend stores a record or what its
// page cursors look like; those are opaque and a backend chooses them.
// Failing exists because the server's error paths (a store that fails
// mid-transaction, a nonce that cannot be taken) cannot be reached from a
// working backend, and a broken database is not a thing a unit test can
// have.
//
// # References
//
//   - Decision record 0031: docs/research/decisions/0031-acme-server-and-state-store.md
//   - Decision record 0032: docs/research/decisions/0032-managed-device-attestation.md
//   - Plan of record: docs/research/implementation_plan.md (section 9, phase 7)
//   - RFC 8555 (ACME): https://www.rfc-editor.org/rfc/rfc8555
//   - Apple: https://support.apple.com/en-gb/guide/deployment/dep28afbde6a/web
package acmetest
