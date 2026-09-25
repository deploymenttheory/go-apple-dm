// Package activationlock implements Apple's server-generated Activation Lock bypass codes.
//
// # Design
//
// Generate returns a cryptographically random code and the hash Apple accepts
// when enabling organization-linked Activation Lock. Hash accepts only the
// canonical server format and applies Apple's encoding and PBKDF2 parameters.
// Device-returned bypass codes remain opaque and must not be passed to Hash.
//
// Callers must securely retain the code before enabling the lock with its hash.
// The hash cannot recover the code. This package performs no network or storage
// operations and its errors omit the supplied code.
//
// # Errors
//
// ErrCode is the catalogued client condition
// DM-ACTIVATIONLOCK-BYPASS-CODE-INVALID.
//
// # References
//
//   - Apple algorithm: https://developer.apple.com/documentation/devicemanagement/creating-and-using-bypass-codes
//   - Protocol helpers: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/protocol-helpers.md
package activationlock
