// Package applications manages macOS installer package metadata, verified content
// revisions and Apple installation manifests.
//
// # Design
//
// Manager separates catalogue records from package bytes. A caller-supplied
// state.Store commits metadata changes and history atomically; BlobStore retains
// private content under immutable revision keys. Revision tokens reject stale
// updates. Metadata can exist before an upload, and each verified content revision
// retains the metadata used when it was created.
//
// Metadata preserves installation preferences, OS requirements, relationships and
// expected digests. Computed hashes, byte counts and verification results belong
// to the uploaded content. Native delivery checks reject installer preferences
// that require behavior outside Apple's MDM and DDM package contracts.
//
// Upload stages bounded input, checks the installer archive and signer trust,
// verifies expected digests, and reads stored bytes back before committing their
// association. SHA-256, SHA-512 and SHA3-512 remain distinct algorithms. MD5 is
// retained for manifest compatibility. Verification records identify checks that
// were performed; signer trust does not establish notarization or revocation.
//
// Manifests identify one content revision and carry its download URL, application
// identity and hashes. Assignment validates those values against the verified
// content. The caller provides HTTPS routing, download authorization, device
// eligibility, command dispatch and observation of installation results.
//
// # References
//
//   - Apple package manifests: https://developer.apple.com/documentation/devicemanagement/manifesturl
//   - Apple enterprise application installation: https://developer.apple.com/documentation/devicemanagement/installenterpriseapplicationcommand
//   - Apple declarative package management: https://github.com/apple/device-management/blob/release/declarative/declarations/configurations/package.yaml
//   - Jamf package metadata: https://developer.jamf.com/jamf-pro/reference/get_v1-packages-id
package applications
