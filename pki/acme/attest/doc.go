// Package attest parses and verifies Managed Device Attestation certificate
// chains and objects.
//
// # Design
//
// Both ACME device-attest-01 and MDM DevicePropertiesAttestation carry a chain
// rooted in Apple's attestation authority. Verification checks trust and
// freshness and can require the attested key to match the key being certified.
// Properties use Apple's documented string and DER-integer encodings. ACME
// freshness derives from the challenge token; MDM responses can use the device's
// documented cache behavior.
//
// A missing required freshness extension fails verification. Other properties
// may be absent, including serial number and UDID under User Enrollment, and
// malformed encodings return errors. The caller decides whether missing identity
// or other properties satisfy admission policy; a trusted chain alone does not
// establish organizational ownership.
//
// # References
//
//   - Decision record 0032: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0032-managed-device-attestation.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md
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
