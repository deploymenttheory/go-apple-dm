// Package proxyserver accepts declarative check-ins forwarded by the MDM role.
//
// # Design
//
// The handler serves POST /v1/declarative-management, decodes the original plist
// and resolves enrollment from its fields. Independent RecvKey/SendKey and shared
// ReplayStore are required. Request authentication binds the versioned envelope;
// response authentication binds that request to the status, content type and body.
// ClientCAs and Auth provide additional checks. Returned status/body retain Apple's
// device-facing semantics.
//
// HTTPS is required, except for the explicit literal-loopback test option. The
// reference server mounts this handler under /ddm/ with shared SQL replay state
// and native TLS on the DDM role. The hop is an internal deployment choice, not a
// separate enrollment protocol.
//
// # References
//
//   - Decision record 0023: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0023-ddm-adapters-and-wire-contract.md
//   - Decision record 0025: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0025-reference-server-roles-and-container.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (private DDM proxy)
//   - E2E scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-010)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - Schema: third_party/apple-device-management/current/mdm/checkin/declarativemanagement.yaml
package proxyserver
