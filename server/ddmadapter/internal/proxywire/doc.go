// Package proxywire is the wire contract between our mdm role and our ddm
// role when they run as separate processes.
//
// # Why
//
// Apple defines the device-facing protocol and nothing about splitting an
// MDM server into processes, so the hop between the roles is ours. It
// carries Apple's message unchanged: one route, a POST whose body is the
// DeclarativeManagement check-in plist exactly as the device sent it, so
// the receiving side resolves the enrollment the same way the device path
// does. Authentication is an HMAC-SHA256 over the body in X-MDM-Signature,
// verified in both directions, with a body limit; mutual TLS and a bearer
// token are layered on by proxyserver. Nothing here is borrowed from
// NanoMDM or MicroMDM. The package is internal to the adapters so the
// contract can change with them.
//
// # References
//
//   - Decision record 0023: docs/research/decisions/0023-ddm-adapters-and-wire-contract.md
//   - Threat model: docs/security/threat-model.md (trust boundary 5)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - RFC 2104 (HMAC): https://www.rfc-editor.org/rfc/rfc2104
package proxywire
