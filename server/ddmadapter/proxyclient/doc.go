// Package proxyclient is the mdm role's egress to a separate ddm role.
//
// # Why
//
// When the engine runs in another process, the mdm role still has to answer
// the device's DeclarativeManagement check-in. This service.DMHandler
// forwards the check-in plist exactly as the device sent it over the
// proxywire contract, signs the request, verifies the response signature,
// bounds the body and the time spent, and relays Apple's status and body
// back untouched: 200 with the JSON, the empty 200 for status, and 404 so
// the device removes a declaration. Upstream failures (transport, 5xx, and
// an authentication failure at the ddm role) surface as CodeInternal
// wrapping ErrUpstream, never as a 404 the device would act on. A base URL
// with a path prefix is joined, not resolved, so no segment is lost.
//
// # References
//
//   - Decision record 0023: docs/research/decisions/0023-ddm-adapters-and-wire-contract.md
//   - Decision record 0025: docs/research/decisions/0025-reference-server-roles-and-container.md
//   - Threat model: docs/security/threat-model.md (trust boundary 5)
//   - E2E scenarios: docs/testing/e2e-scenarios.md (E2E-010)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
package proxyclient
