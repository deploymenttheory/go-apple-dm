// Package support answers "is this key supported on this OS, version,
// channel, and enrollment context?" at runtime, from tables generated out
// of the supportedOS blocks in Apple's device management schema.
//
// # Why
//
// Apple's YAML records, per key, the OS version that introduced it,
// deprecated it, or removed it, whether it needs supervision, and whether
// it is allowed on user enrollment, Shared iPad, or the user channel.
// Most implementations drop that data at generation time; decision record
// 0003 keeps it, so a server can refuse to send a key the target device
// will reject and an operator tool can explain a command. Generated
// packages register their tables in init; callers query them through
// Lookup or, indirectly, through the generated Validate methods when given
// a Target. Phase 1 of the plan of record delivers the tables with the
// schema packages, and phase 8's mdmctl explain reads them.
//
// The package holds the query logic and the types; the tables themselves
// are generated and never hand-edited.
//
// # References
//
//   - Decision record 0003: docs/research/decisions/0003-schema-generator.md
//   - Plan of record: docs/research/implementation_plan.md (section 2, the generator; phase 1)
//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement
//   - Schema: third_party/device-management/docs/schema.yaml (supportedOS)
package support
