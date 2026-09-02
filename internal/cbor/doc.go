// Package cbor decodes and encodes the small, strict subset of CBOR that
// Apple's Managed Device Attestation objects use.
//
// # Why
//
// A device answering an ACME device-attest-01 challenge sends a WebAuthn
// attestation object: CBOR holding a format name and an attestation
// statement whose x5c member is the certificate chain. That is the first
// CBOR this project has ever had to read, it arrives from an unauthenticated
// caller, and the plan of record forbids new module dependencies, so this
// package exists instead of a general CBOR library.
//
// It is deliberately small. Definite lengths only, text keys only, no tags,
// no floating point, no indefinite-length strings, no duplicate keys, a
// nesting limit, a size limit, and no trailing data. Everything outside the
// subset is an error rather than a best-effort decode, because a general
// decoder is a larger attack surface than the format needs. Unknown members
// of a map are skipped so that a future Apple addition (an authData member,
// say) does not break enrollment. Marshal emits the same subset with map
// keys in the deterministic order of RFC 8949 section 4.2.1, which the
// simulator and the tests use to build attestation objects.
//
// # References
//
//   - Decision record 0032: docs/research/decisions/0032-managed-device-attestation.md
//   - Plan of record: docs/research/implementation_plan.md (phase 7)
//   - RFC 8949 (Concise Binary Object Representation): https://www.rfc-editor.org/rfc/rfc8949
//   - W3C WebAuthn attestation objects: https://www.w3.org/TR/webauthn-2/#sctn-attestation
//   - Apple: https://developer.apple.com/documentation/devicemanagement/acmecertificate
//   - Schema: third_party/device-management/mdm/profiles/com.apple.security.acme.yaml
package cbor
