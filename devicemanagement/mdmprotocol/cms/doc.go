// Package cms signs and verifies attached and detached CMS signatures used by
// Apple device management, and decrypts CMS envelopes.
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
// DecryptEnvelope handles BER/DER EnvelopedData with a matching RSA recipient.
// The caller authenticates the enclosing MDM response and retains the original
// reply key for delayed FileVault results. Certificate expiry does not prevent
// decrypting an older envelope; decryption alone does not prove its sender.
//
// # Errors
//
// Every sentinel is a device condition or an operator condition classified
// through devicemanagement/fault. A malformed header, structure or unsupported
// algorithm is InvalidArgument; a signature, chain, signing-time or signer-count
// failure is PermissionDenied, because the device presented an identity the
// server does not accept. Signing, recipient and decryption failures are the
// operator's.
//
// # References
//
//   - Decision record 0006: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0006-mdm-signature-verification.md
//   - Decision record 0009: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0009-enrollment-profiles.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (/checkin and /connect rows)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
//   - Schema: third_party/apple-device-management/current/mdm/profiles/com.apple.mdm.yaml (SignMessage)
//   - RFC 5652 (Cryptographic Message Syntax): https://www.rfc-editor.org/rfc/rfc5652
package cms
