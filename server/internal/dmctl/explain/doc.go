// Package explain resolves compiled schema identifiers and renders their support
// metadata.
//
// # Design
//
// Resolution covers Go names, wire identifiers and dotted paths across generated
// families. Ambiguity returns all matches, and suggestions help locate nearby
// identifiers. Unspecified tri-state values render as a dash; missing support or
// target OS renders as unknown. Reasons come directly from schema/support.
//
// The package reads compiled tables and creates no HTTP client. It describes the
// binary's schema pin rather than live documentation or observed device
// capabilities.
//
// # References
//
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0036-dmctl-explain-over-schema-support.md
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0035-dmctl-structure-and-credentials.md
//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md
//   - Schema: third_party/device-management/docs/schema.yaml (supportedOS)
package explain
