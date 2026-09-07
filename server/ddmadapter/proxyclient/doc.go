// Package proxyclient forwards MDM declarative check-ins to a separately
// deployed declaration engine.
//
// # Design
//
// The service.DMHandler forwards original plist bytes, applies configured
// request signing and response verification, and bounds response size and time.
// Device-facing 200, empty status responses and declaration 404 responses are
// preserved. Transport, authentication and upstream server failures become
// internal service errors rather than declaration-removal responses. URL joining
// preserves a configured path prefix.
//
// Callers configure authentication and transport protection. The reference
// server requires both request and response HMAC keys for the split hop.
//
// # References
//
//   - Decision record 0023: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0023-ddm-adapters-and-wire-contract.md
//   - Decision record 0025: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0025-reference-server-roles-and-container.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (private DDM proxy)
//   - E2E scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-010)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
package proxyclient
