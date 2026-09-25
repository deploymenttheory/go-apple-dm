// Package manifest builds installation manifests from caller-supplied asset bytes.
//
// # Design
//
// Build streams SHA-256 hashes into the generated ManifestURL type. A zero chunk
// size selects a whole-file digest; a positive size includes each chunk and the
// final short chunk. The asset byte count is returned separately from the wire
// manifest. Reader failures return no partial manifest.
//
// The caller supplies an HTTPS URL and package or enterprise-app metadata. The
// helper neither downloads assets nor inspects archives, hosts manifests or
// installs software. It emits no MD5 hashes or removed manifest fields.
//
// # Errors
//
// ErrInput is the catalogued client condition DM-MANIFEST-INPUT-INVALID.
//
// # References
//
//   - Apple manifest fields: https://developer.apple.com/documentation/devicemanagement/manifesturl/itemsitem/assetsitem
//   - Apple package installation: https://developer.apple.com/documentation/devicemanagement/installing-packages
//   - Apple app installation: https://developer.apple.com/documentation/devicemanagement/install-application-command
package manifest
