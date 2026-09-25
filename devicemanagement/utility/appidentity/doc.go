// Package appidentity inspects macOS application bundles and executable
// code-signing identities for use when authoring device management configurations.
//
// # Design
//
// Inspect reads bundle metadata from Info.plist and enumerates the main
// executable's architectures with lipo. For each architecture, codesign supplies
// the signing identifier, team identifier, CDHash and designated requirement.
// Strict verification and signing-requirement checks determine signature status
// and Apple, Developer ID or App Store category. Unmatched categories remain
// unknown. Bundle identifiers and signing identifiers are separate observations.
//
// Native inspection requires macOS, codesign and lipo; Apple Command Line Tools
// provide lipo where needed. Subprocesses have bounded output and deadlines.
// Inspection reads the main executable without launching it or traversing
// embedded helpers. Verification does not establish Gatekeeper acceptance or
// notarization.
//
// ReadBundle and ReadExecutable parse immutable bytes on any platform through
// go-macos-pkg. They retain every architecture and supported code directory,
// including full hashes and Apple's twenty-byte CDHashes. Parsed signatures have
// NotChecked status and Unknown category; reading metadata does not establish
// integrity, certificate trust, Gatekeeper acceptance or notarization. When a
// slice has multiple code directories, callers select an algorithm explicitly.
//
// Inspection returns observed identifiers and preserves an absent team
// identifier. Callers choose the matching identifiers and constraints for their
// existing payload types; inspection does not choose allow or deny policy.
//
// # Errors
//
// ErrInput and ErrTooLarge are the catalogued client conditions
// DM-APPIDENTITY-INVALID and DM-APPIDENTITY-TOO-LARGE. ErrUnsupported and
// ErrInspect are the operator's.
//
// # References
//
//   - Utility guide: https://github.com/deploymenttheory/go-apple-dm/blob/main/devicemanagement/utility/README.md
//   - Apple code-signing hashes: https://developer.apple.com/documentation/technotes/tn3126-inside-code-signing-hashes
//   - Apple code-signing requirements: https://developer.apple.com/library/archive/technotes/tn2206/_index.html
//   - Apple tools: codesign(1), lipo(1)
package appidentity
