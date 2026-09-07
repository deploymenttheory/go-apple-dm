// Package conformance supplies encoding round-trip and validation helpers for
// generated schema tests.
//
// # Design
//
// Generated cases populate documented keys and compare decoded generic values
// across JSON, XML plist and binary plist as applicable. The helpers centralize
// checks so every family uses the same rules, including independent binary
// encoding and explicit JSON-policy tests. They verify modeled type/encoding
// behavior, not device installation or protocol admission. The package is
// internal to schema and contains test support only.
//
// # References
//
//   - Decision record 0003: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0003-schema-generator.md
//   - Decision record 0018: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0018-go-1.27-baseline.md
//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md
//   - Schema: third_party/device-management/docs/schema.yaml (meta-schema)
package conformance
