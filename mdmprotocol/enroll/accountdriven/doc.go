// Package accountdriven implements account-driven Device Enrollment and
// account-driven User Enrollment authentication.
//
// # Design
//
// The handlers implement apple-as-web and apple-oauth2 challenges and profile
// retrieval. Access bearers are reusable until expiry or invalidation;
// authorization codes are one-use and refresh tokens rotate atomically. Trusted
// issuance associates an identity certificate with the profile reference,
// subject, issuer, Managed Apple Account and platform. The first successful
// Authenticate confirms the reserved enrollment identifier.
//
// Ongoing macOS device-channel requests omit the bearer; macOS user channels and
// supported iOS, iPadOS and visionOS channels verify it. Reauthentication is
// limited to recognized account-driven sessions. Body parsing, identity
// verification and profile composition are injected. Legacy query credentials
// are rejected, and association reservation/confirmation spans separate stores.
//
// # References
//
//   - Decision record 0047: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0047-enrollment-authentication-and-optional-security-services.md
//   - Decision record 0028: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0028-account-driven-enrollment-and-service-discovery.md
//   - Decision record 0027: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md (body verification)
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (account-driven enrollment)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment
//   - Apple: https://developer.apple.com/documentation/devicemanagement/implementing-the-simple-authentication-account-driven-enrollment-flow
//   - Apple: https://developer.apple.com/documentation/devicemanagement/implementing-the-oauth2-authentication-account-driven-enrollment-flow
//   - Schema: third_party/device-management/mdm/profiles/com.apple.mdm.yaml (EnrollmentMode, AssignedManagedAppleID)
//   - RFC 6749 (OAuth 2.0), RFC 6750 (bearer tokens): https://www.rfc-editor.org/rfc/rfc6749, https://www.rfc-editor.org/rfc/rfc6750
package accountdriven
