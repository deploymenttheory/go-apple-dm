// Package validation collects schema validation results for generated
// types: a Collector the generated Validate methods report into and the
// Error and Errors types callers inspect.
//
// # Why
//
// Apple's schema declares, per key, presence, allowed values, numeric
// ranges, formats, and repetition limits. Phase 1 of the plan of record
// generates a Validate method from those constraints (decision record
// 0003), and the value of that method is in reporting everything wrong
// with a value in one pass rather than stopping at the first fault, so an
// operator fixing a profile or a declaration sees the whole list.
// Generated methods call the Collector for every key with the rule and the
// key's support entry; given a Target, the Collector also records whether
// the target OS version and enrollment context accept each key.
//
// The package defines the vocabulary and the accumulator only. The rules
// are generated, and the support tables live in schema/support.
//
// # References
//
//   - Decision record 0003: docs/research/decisions/0003-schema-generator.md
//   - Decision record 0004: docs/research/decisions/0004-checkin-and-command-core.md (validated command payloads)
//   - Decision record 0009: docs/research/decisions/0009-enrollment-profiles.md (validated profiles)
//   - Plan of record: docs/research/implementation_plan.md (section 2, the generator; phase 1)
//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md
//   - Schema: third_party/device-management/docs/schema.yaml (meta-schema)
package validation
