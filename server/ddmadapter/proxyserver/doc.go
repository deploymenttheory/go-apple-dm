// Package proxyserver accepts declarative check-ins forwarded by the MDM role.
//
// # Design
//
// The handler serves POST /v1/declarative-management, decodes the original plist
// and resolves enrollment from its fields. Configured RecvKey, ClientCAs and
// Auth checks authenticate the caller. SendKey signs status and body for every
// response. The returned body and status retain Apple's device-facing semantics.
//
// Authentication options are explicit library settings; configure the controls
// required at the deployment boundary. The reference server mounts this handler
// under /ddm/ and requires HMAC keys. The hop is an internal deployment choice,
// not a separate enrollment protocol.
//
// # References
//
//   - Decision record 0023: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0023-ddm-adapters-and-wire-contract.md
//   - Decision record 0025: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0025-reference-server-roles-and-container.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (private DDM proxy)
//   - E2E scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-010)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - Schema: third_party/device-management/mdm/checkin/declarativemanagement.yaml
package proxyserver
