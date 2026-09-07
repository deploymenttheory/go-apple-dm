// Package httpapi exposes MDM check-in and command handlers and
// identity-certificate middleware.
//
// # Design
//
// Handler dispatches PUT plist requests by content type, allowing check-in and
// server URLs to share a route. Certificate middleware extracts identity from
// Mdm-Signature, TLS or a configured proxy header for service authorization.
// Trust roots and proxy access controls must be configured; a header alone does
// not prove key possession.
//
// Traditional MDM errors avoid 401. Recognized account-driven sessions can
// return Apple's typed reauthentication challenge so the device can retry.
// Unknown enrollments can receive the configured unrecognized-device response.
// Enrollment profile delivery belongs to mdmprotocol/enroll and
// server/internal/app.
//
// # References
//
//   - Decision record 0004: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0004-checkin-and-command-core.md
//   - Decision record 0006: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0006-mdm-signature-verification.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (/checkin and /connect rows)
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-001 to E2E-005)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-connections
//   - Schema: third_party/device-management/mdm/checkin/*.yaml
//   - Schema: third_party/device-management/mdm/errors/*.yaml
package httpapi
