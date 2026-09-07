// Package enroll builds MDM enrollment profiles and serves the over-the-air
// Profile Service protocol.
//
// # Design
//
// The builder combines MDM settings, a SCEP, ACME or pre-issued PKCS #12
// identity, and optional trust anchors, then validates against generated schema
// types. Profile composition/signing uses mdmprotocol/profile and certificate
// issuance uses the PKI packages.
//
// OTAService distinguishes device-signed and enrollment-identity-signed protocol
// phases and calls injected authorization and profile callbacks. Automated
// Device Enrollment and account-driven enrollment have separate handler
// packages. Callers provide admission policy, trust roots and stable profile
// identifiers.
//
// # References
//
//   - Decision record 0009: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0009-enrollment-profiles.md
//   - Decision record 0010: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0010-ota-profile-service.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (OTA profile-service and Enrollment profile rows)
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-006, E2E-016)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/mdm
//   - Apple: https://developer.apple.com/documentation/devicemanagement/scep
//   - Apple: https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles
//   - Apple: https://developer.apple.com/library/archive/documentation/NetworkingInternet/Conceptual/iPhoneOTAConfiguration/
//   - Schema: third_party/device-management/mdm/profiles/TopLevel.yaml, CommonPayloadKeys.yaml
//   - Schema: third_party/device-management/mdm/profiles/com.apple.mdm.yaml, com.apple.security.scep.yaml, com.apple.security.root.yaml
package enroll
