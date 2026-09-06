// Package attest reads and verifies Apple's Managed Device Attestation:
// the certificate chain a device produces to prove that a key was generated
// in its Secure Enclave and to describe the hardware it lives on.
//
// # Why
//
// Two Apple surfaces carry the same artefact. A device answering an ACME
// device-attest-01 challenge sends a WebAuthn attestation object whose
// statement holds the chain, and a device answering a DeviceInformation
// query with DevicePropertiesAttestation sends the same chain as an array
// of DER certificates. Both root at the Apple Enterprise Attestation Root
// CA, both carry the device's properties in custom extensions on the leaf,
// and both bind a freshness code chosen by the server. Phase 7 of the plan
// of record needs the verification once, so it lives here and the ACME
// server and the service layer both call it.
//
// The package is deliberately strict where the reference implementations
// are not, because each of these is a way in:
//
//   - The freshness code is required, not optional. Apple defines it as the
//     SHA-256 of the challenge token, which is what stops an attestation
//     from a previous order being replayed into this one. step-ca skips the
//     comparison when the extension is absent, so an attestation carrying no
//     freshness extension at all passes its check.
//   - The attested key is compared with the key being certified. Apple's
//     guidance is to retain the leaf's public key for a later validation,
//     and that later validation is this one: without it, anyone who can
//     observe a valid attestation can have an unrelated key certified.
//   - Every documented extension is parsed by its documented type. The
//     identity and version extensions hold bare UTF-8, but the System
//     Integrity Protection and kernel extension statuses hold DER integers,
//     so reading them all as raw bytes yields a value that looks like data
//     but is not.
//
// Absent extensions are not errors. Apple omits the serial number and UDID
// for a user enrollment, and states that a property its attestation servers
// cannot verify may be blank or missing. What to do about a missing
// property is a policy question, so this package reports what it found and
// leaves the decision to the caller.
//
// # References
//
//   - Decision record 0032: docs/research/decisions/0032-managed-device-attestation.md
//   - Plan of record: docs/research/implementation_plan.md (phase 7)
//   - Threat model: docs/security/threat-model.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/acmecertificate
//   - Apple: https://developer.apple.com/documentation/devicemanagement/deviceinformationresponse
//   - Apple: https://support.apple.com/guide/deployment/managed-device-attestation-dep28afbde6a/web
//   - Apple certificate authority: https://www.apple.com/certificateauthority/private/
//   - Schema: third_party/device-management/mdm/profiles/com.apple.security.acme.yaml
//   - Schema: third_party/device-management/mdm/commands/information.device.yaml
//   - Schema: third_party/device-management/declarative/declarations/assets/credentials/acme.yaml
//   - RFC 8555 (ACME): https://www.rfc-editor.org/rfc/rfc8555
//   - draft-ietf-acme-device-attest: https://datatracker.ietf.org/doc/draft-acme-device-attest/
package attest
