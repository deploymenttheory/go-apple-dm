// Package validation collects constraint failures from generated schema
// validators.
//
// # Design
//
// Generated Validate methods report presence, range, format, value and
// repetition checks to Collector. A supplied support.Target also enables
// platform and enrollment-context checks for populated fields. Aggregating
// errors lets callers inspect multiple invalid values in one pass.
//
// This package defines the result types and accumulation behavior. The generator
// defines schema-specific rules, and schema/support holds availability metadata.
// Successful structural validation does not prove a payload will install on a
// physical device.
//
// # References
//
//   - Decision record 0003: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0003-schema-generator.md
//   - Decision record 0004: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0004-checkin-and-command-core.md (validated command payloads)
//   - Decision record 0009: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0009-enrollment-profiles.md (validated profiles)
//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md
//   - Schema: third_party/device-management/docs/schema.yaml (meta-schema)
package validation
