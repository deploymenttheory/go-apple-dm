// Package acme serves the ACME protocol used by Apple device identity payloads,
// including device-attest-01.
//
// # Design
//
// The server implements directory, nonce, account, order, authorization,
// challenge, finalize and certificate endpoints for a permanent-identifier.
// Client identifiers carry expected device binding and expiry and are consumed
// by the first order atomically. Attestation is checked against that binding and
// re-verified against the CSR key before issuance. Policy decides whether
// verified properties authorize enrollment; verification alone does not
// establish ownership.
//
// Binding.UDID denotes the attested identifier (ProvisioningUDID on macOS).
// Binding.MDMUDID separately records the MDM identifier for admission and
// provenance. AuthorizeUnattested is an optional alternative authorization
// boundary, checked at challenge validation and finalization; it never skips
// the ordinary authorization policy.
//
// An optional registry provides revokeCert with certificate-key, issuing-account
// or all-identifier authorization. Account key rollover is not implemented.
// Store interfaces isolate persistence, and caller-supplied public URLs define
// JWS URL binding behind proxies.
//
// # References
//
//   - Decision record 0031: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0031-acme-server-and-state-store.md
//   - Decision record 0032: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0032-managed-device-attestation.md
//   - Decision record 0033: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0033-acme-identity-in-profiles-and-ddm.md
//   - Decision record 0008: the certificate authority abstraction issuance goes through
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/acmecertificate
//   - Schema: third_party/device-management/mdm/profiles/com.apple.security.acme.yaml
//   - RFC 8555 (ACME): https://www.rfc-editor.org/rfc/rfc8555
//   - draft-ietf-acme-device-attest: https://datatracker.ietf.org/doc/draft-acme-device-attest/
//   - RFC 7515 (JWS), RFC 7638 (JWK thumbprint), RFC 4043 (permanent identifier)
package acme
