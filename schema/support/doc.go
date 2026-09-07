// Package support queries generated platform, version, channel and
// enrollment-context metadata.
//
// # Design
//
// Generated packages register supportedOS tables. Lookup, Families and Paths
// expose those entries, and Check evaluates a Target with a reason and
// deprecation information. Generated validators and dmctl explain use the same
// metadata. Pointer booleans preserve the difference between an unspecified
// condition and an explicit prohibition.
//
// The tables describe the pinned Apple schema. Missing support or target data
// limits what can be inferred; consumers must inspect the result reason rather
// than treat an unspecified target as verified compatibility.
//
// # References
//
//   - Decision record 0003: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0003-schema-generator.md
//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement
//   - Schema: third_party/device-management/docs/schema.yaml (supportedOS)
package support
