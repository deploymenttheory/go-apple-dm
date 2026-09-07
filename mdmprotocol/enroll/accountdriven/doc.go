// Package accountdriven implements Apple's account-driven enrollment: the
// first enrollment attempt, the 401 challenge, both documented
// authentication flows, and the tokens that carry the authenticated
// identity into the enrollment profile and the check-in.
//
// # Why
//
// Account-driven enrollment lets a person enrol by signing in with a
// Managed Apple Account: the device discovers the server (enroll/discovery),
// posts a signed device description, receives a 401 whose
// WWW-Authenticate header selects a flow, authenticates in a web view
// (apple-as-web) or through an OAuth 2 authorization server we run
// (apple-oauth2), and posts again with a bearer token to receive the
// profile. Phase 6 uses a reusable access bearer plus certificate-backed
// enrollment associations (record 0047 amends the original token design).
// Authorization codes are consumed once; refresh tokens rotate atomically.
// macOS device channels omit the bearer as Apple documents; macOS user
// channels and iOS/iPadOS/visionOS channels verify it on ongoing requests.
// The body parser and identity verifier are injected, and the
// profile comes from the enroll builder with EnrollmentMode and
// AssignedManagedAppleID enforced.
//
// # References
//
//   - Decision record 0047: docs/research/decisions/0047-enrollment-authentication-and-optional-security-services.md
//   - Decision record 0028: docs/research/decisions/0028-account-driven-enrollment-and-service-discovery.md
//   - Decision record 0027: docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md (body verification)
//   - Plan of record: docs/research/implementation_plan.md (phase 6)
//   - Threat model: docs/security/threat-model.md (trust boundary 9)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment
//   - Apple: https://developer.apple.com/documentation/devicemanagement/implementing-the-simple-authentication-account-driven-enrollment-flow
//   - Apple: https://developer.apple.com/documentation/devicemanagement/implementing-the-oauth2-authentication-account-driven-enrollment-flow
//   - Schema: third_party/device-management/mdm/profiles/com.apple.mdm.yaml (EnrollmentMode, AssignedManagedAppleID)
//   - RFC 6749 (OAuth 2.0), RFC 6750 (bearer tokens): https://www.rfc-editor.org/rfc/rfc6749, https://www.rfc-editor.org/rfc/rfc6750
package accountdriven
