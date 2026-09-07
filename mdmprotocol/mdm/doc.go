// Package mdm defines enrollment identities and decodes Apple MDM check-in,
// command and response envelopes.
//
// # Design
//
// Generated registries supply message and payload types; this package adds
// MessageType dispatch, typed command construction and the surrounding protocol
// fields. Enrollment.Resolve distinguishes device, user, Shared iPad and User
// Enrollment channels. Original message bytes are retained for signature and
// forwarding paths.
//
// The package does not authorize requests or persist enrollment state.
// server/service applies lifecycle and admission policy, storage defines
// persistence contracts, and server/httpapi provides transport handling.
//
// # References
//
//   - Decision record 0002: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0002-plist-library.md
//   - Decision record 0004: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0004-checkin-and-command-core.md
//   - Decision record 0006: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0006-mdm-signature-verification.md (request identity)
//   - Decision record 0016: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0016-user-authenticate-state.md (UserAuthenticate responses)
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (/checkin and /connect rows)
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-001 to E2E-005)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/commands-and-queries
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
//   - Schema: third_party/device-management/mdm/checkin/*.yaml
//   - Schema: third_party/device-management/mdm/commands/*.yaml
package mdm
