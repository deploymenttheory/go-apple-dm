// Package cms signs and verifies attached and detached CMS signatures used by
// Apple device management.
//
// # Design
//
// Detached signatures authenticate Mdm-Signature request bodies; attached
// signatures carry configuration-profile content. Verification requires one
// signer and supports explicit trust roots, an injected clock and configured
// signing-time tolerance. The tolerant path still validates digest, attributes,
// signature and chain.
//
// A valid signature proves key possession, not authorization for an enrollment.
// HTTP certificate extraction and service pinning apply that separate policy.
// Callers select trust roots and whether profile parsing requires a signature.
//
// # References
//
//   - Decision record 0006: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0006-mdm-signature-verification.md
//   - Decision record 0009: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0009-enrollment-profiles.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (/checkin and /connect rows)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
//   - Schema: third_party/device-management/mdm/profiles/com.apple.mdm.yaml (SignMessage)
//   - RFC 5652 (Cryptographic Message Syntax): https://www.rfc-editor.org/rfc/rfc5652
package cms
