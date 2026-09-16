// Package replycerts provisions and retains FileVault encryption identities.
//
// # Design
//
// Prepare generates an RSA certificate and private key entirely in Go, persists
// them, then supplies the DER certificate to RotateFileVaultKey. Personal
// rotations use ReplyEncryptionCertificate; institutional rotations use
// NewCertificate. No shell command or operating-system certificate tool is used.
// EscrowProfile prepares an InstallProfile command with a FileVault recovery-key
// escrow payload referencing its generated certificate in the same profile.
//
// Enrollment identity and command UUID bind each key to its command. Atomic
// state updates make concurrent retries reuse the same identity and reject
// conflicting command bodies or externally supplied certificates. New commands
// receive separate keys. Persistent adapters must encrypt records at rest.
//
// Recipient retrieves the retained key for privileged response processing, even
// after certificate expiry. Forget is explicit: retain escrow identities while
// their profiles or encrypted keys remain in use, and retain command identities
// until all replies are recovered or deliberately abandoned. Automatic expiry
// must not destroy decryption keys. The reference server prepares certificates
// before enqueueing through its administrative API. Embedders call Prepare or
// EscrowProfile before Core.Enqueue.
//
// # References
//
//   - Apple rotation certificate fields: https://developer.apple.com/documentation/devicemanagement/rotatefilevaultkeycommand/command-data.dictionary
//   - Apple encrypted result: https://developer.apple.com/documentation/devicemanagement/rotatefilevaultkeyresponse/rotateresult-data.dictionary
//   - Apple escrow payload: https://developer.apple.com/documentation/devicemanagement/fderecoverykeyescrow
//   - macOS 26 bootstrap-token rotation: https://support.apple.com/en-us/124963
//   - Protocol helpers: docs/operations/protocol-helpers.md (relative to repository root)
package replycerts
