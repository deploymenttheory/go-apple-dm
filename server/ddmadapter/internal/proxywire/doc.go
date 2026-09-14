// Package proxywire defines the internal protocol between separately deployed
// MDM and declaration-engine roles.
//
// # Design
//
// POST /v1/declarative-management carries the original DeclarativeManagement
// check-in plist. Versioned request HMACs bind method, target, content type,
// timestamp, nonce and body. Response HMACs bind the request envelope, status,
// content type and body. Verification checks five-minute freshness and atomically
// claims nonces in a caller-supplied shared store for ten minutes. Shared helpers
// apply body limits and signature encoding. Proxyserver can additionally require
// mutual TLS or another authorization check.
//
// This is a project-specific deployment protocol, not an Apple or NanoMDM
// transport contract. The adapters resolve enrollment from the forwarded
// message. HMAC does not encrypt data; the adapters require HTTPS, with an explicit
// literal-loopback exception for tests. Replicas must share replay state.
//
// # References
//
//   - Decision record 0023: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0023-ddm-adapters-and-wire-contract.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (private DDM proxy)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - RFC 2104 (HMAC): https://www.rfc-editor.org/rfc/rfc2104
package proxywire
