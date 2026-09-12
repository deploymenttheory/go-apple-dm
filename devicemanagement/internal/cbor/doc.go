// Package cbor encodes and decodes the CBOR subset used by Managed Device
// Attestation objects.
//
// # Design
//
// The decoder accepts definite-length values with text map keys and bounds size
// and nesting. Tags, floating-point values, indefinite lengths, duplicate keys
// and trailing data are rejected. Attestation parsing selects the required
// format and certificate-chain members from the decoded map.
//
// Marshal emits the same subset with deterministic map ordering from RFC 8949.
// This bounded format supports the attestation parser, simulator and tests; it
// is not a general CBOR implementation or a WebAuthn ceremony implementation.
//
// # References
//
//   - Decision record 0032: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0032-managed-device-attestation.md
//   - RFC 8949 (Concise Binary Object Representation): https://www.rfc-editor.org/rfc/rfc8949
//   - W3C WebAuthn attestation objects: https://www.w3.org/TR/webauthn-2/#sctn-attestation
//   - Apple: https://developer.apple.com/documentation/devicemanagement/acmecertificate
//   - Schema: third_party/device-management/mdm/profiles/com.apple.security.acme.yaml
package cbor
