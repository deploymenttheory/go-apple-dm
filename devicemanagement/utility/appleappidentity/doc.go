// Package appleappidentity discovers bundle identifiers from Apple's iPhone
// and iPad app catalogue for use when authoring device management configurations.
//
// # Design
//
// Search matches app names and bundle identifiers in a bundled snapshot of
// Apple's published catalogue. Lookup requires an exact, case-sensitive bundle
// identifier. Results preserve the source spelling and do not share mutable
// storage with the catalogue. SourceURL and ReviewedOn identify its provenance.
//
// The catalogue includes preinstalled and downloadable Apple apps. Entries do
// not establish installation, removability, availability on a particular device
// or OS release, or identifiers for macOS apps. Callers select entries for their
// existing payload types. Catalogue queries perform no network or filesystem I/O.
//
// # References
//
//   - Utility guide: https://github.com/deploymenttheory/go-apple-dm/blob/main/devicemanagement/utility/README.md
//   - Apple iPhone and iPad app bundle IDs: https://support.apple.com/en-euro/guide/deployment/depece748c41/web
package appleappidentity
