// Package appartifact discovers application identities in macOS distribution
// artifacts for device management configuration authoring.
//
// # Design
//
// Inspect accepts an immutable local PKG, DMG, ZIP, or Mach-O file. Package
// payloads are decoded with go-macos-pkg; APFS and HFS+ images are read through
// go-apfs-v2 without mounting. Application bundles are inspected with the
// portable appidentity reader. Scripts are never executed. Apps downloaded or
// created by an installer script cannot be discovered from its payload.
//
// Results identify the original artifact by SHA-256 and retain container-relative
// locations. Those locations are provenance, not installation paths or DDM path
// constraints. Multiple applications remain selectable. Per-application failures
// are reported explicitly and make Complete false. No allow or deny policy is
// chosen, and extracted signing metadata is not signature verification.
//
// Uploads, expanded data, entries, applications and nested containers have
// configurable bounds. Temporary payloads are removed when inspection returns.
// Callers own access control and should limit concurrent inspection requests.
//
// # Errors
//
// ErrInvalid, ErrUnsupported and ErrLimit are the catalogued client conditions
// DM-APPARTIFACT-INVALID, DM-APPARTIFACT-UNSUPPORTED and DM-APPARTIFACT-LIMIT.
//
// # References
//
//   - Application identity guide: https://github.com/deploymenttheory/go-apple-dm/blob/main/devicemanagement/utility/README.md
//   - Apple image readers: https://github.com/deploymenttheory/go-apfs-v2
//   - Apple package readers: https://github.com/deploymenttheory/go-macos-pkg
package appartifact
