// Package proxyserver accepts declarative check-ins forwarded by a proxyclient
// in a custom composition with a remote declaration engine.
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
// embedding application chooses the listener, route prefix, replay store, and
// TLS configuration. The unified reference server uses the in-process adapter;
// it does not expose this proxy endpoint. The hop is a project transport for
// the same Apple declarative check-in protocol.
//
// # References
//
//   - Decision record 0023: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0023-ddm-adapters-and-wire-contract.md
//   - Decision record 0025: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0025-reference-server-roles-and-container.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (private DDM proxy)
//   - Adapter contract tests: https://github.com/deploymenttheory/go-apple-dm/blob/main/server/ddmadapter/proxyserver/proxyserver_test.go
//   - Reference composition: https://github.com/deploymenttheory/go-apple-dm/blob/main/server/internal/app/doc.go
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - Schema: third_party/apple-device-management/current/mdm/checkin/declarativemanagement.yaml
package proxyserver
