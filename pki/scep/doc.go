// Package scep serves SCEP enrollment and renewal and provides a client for
// identity issuance.
//
// # Design
//
// The handler implements GetCACert, GetCACaps and PKIOperation through
// ca.Signer. Challenge providers support static, one-time and expiring HMAC
// credentials, and a CSR verifier can apply admission policy. Renewal challenge
// bypass requires a signer trusted by the CA with the same subject as the CSR.
// Optional certificate status checks apply before renewal and cannot be bypassed
// with a shared challenge.
//
// Bodies are bounded. Parse failures return HTTP errors; protocol policy
// refusals produce signed failure CertReps. Key policy belongs to ca and profile
// composition to mdmprotocol/enroll. An RSA recipient key is required for SCEP
// envelope decryption.
//
// # References
//
//   - Decision record 0008: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0008-scep-and-ca.md
//   - Decision record 0009: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0009-enrollment-profiles.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (SCEP rows)
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-006, E2E-016)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/scep
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
//   - Schema: third_party/device-management/mdm/profiles/com.apple.security.scep.yaml
//   - RFC 8894 (Simple Certificate Enrolment Protocol): https://www.rfc-editor.org/rfc/rfc8894
package scep
