// Package acme is an ACME server for Apple device identity certificates:
// the subset of RFC 8555 that Apple's ACME payload uses, with the
// device-attest-01 challenge, Managed Device Attestation, and policy hooks
// that decide which devices may enroll.
//
// # Why
//
// SCEP issues a device its MDM identity against a shared challenge password,
// which is the whole of the device's authentication. Apple's ACME payload
// replaces that with a key generated in the Secure Enclave and an attestation
// from Apple's servers describing the hardware it lives on. This package is
// the server side of that exchange.
//
// It implements what Apple's client actually uses and nothing else: a
// directory, nonces, accounts, orders for a single permanent-identifier,
// one device-attest-01 challenge per order, finalize, and certificate
// download. Certificate revocation and account key rollover are absent
// because the device never asks for them, and an endpoint that is never
// exercised is an endpoint whose faults are never found.
//
// Three decisions shape the rest:
//
// The client identifier is one time. Apple describes it as an anti-replay
// code, and the first order to use one claims it in the same transaction
// that creates the order, so a second order for the same identifier is
// refused even under a race.
//
// The identifier and the attestation are cross-checked. The identifier says
// which device the server expected; the attestation says which device
// turned up. A binding that names a serial number or UDID must match what
// Apple attested, so intercepting an identifier is not enough to obtain a
// certificate with it.
//
// Issuance is a policy decision, not a protocol outcome. Verifying an
// attestation proves a genuine Apple device produced a key; it says nothing
// about whether that device belongs to this organisation. The Policy hook
// runs after verification with the attested facts in hand, which is the
// seam the reference implementations lack and the reason step-ca's
// documentation warns that it trusts any Apple device.
//
// # References
//
//   - Decision record 0031: docs/research/decisions/0031-acme-server-and-state-store.md
//   - Decision record 0032: docs/research/decisions/0032-managed-device-attestation.md
//   - Decision record 0033: docs/research/decisions/0033-acme-identity-in-profiles-and-ddm.md
//   - Decision record 0008: the certificate authority abstraction issuance goes through
//   - Plan of record: docs/research/implementation_plan.md (phase 7)
//   - Threat model: docs/security/threat-model.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/acmecertificate
//   - Schema: third_party/device-management/mdm/profiles/com.apple.security.acme.yaml
//   - RFC 8555 (ACME): https://www.rfc-editor.org/rfc/rfc8555
//   - draft-ietf-acme-device-attest: https://datatracker.ietf.org/doc/draft-acme-device-attest/
//   - RFC 7515 (JWS), RFC 7638 (JWK thumbprint), RFC 4043 (permanent identifier)
package acme
