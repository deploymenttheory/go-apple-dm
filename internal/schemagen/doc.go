// Package schemagen turns Apple's device management YAML schema into the
// Go packages under schema/: a strict loader, an intermediate model, and
// emitters for types, registries, Validate methods, support tables, and
// conformance tests.
//
// # Why
//
// Apple publishes the wire format of every command, check-in message,
// profile payload, declaration, and status item as YAML in
// https://github.com/apple/device-management, vendored under
// third_party/device-management at a pinned commit. Hand-writing hundreds
// of structs from it would drift; phase 1 of the plan of record instead
// generates them (decision record 0003). The loader is strict: any key not
// modelled here fails, so Apple additions surface as explicit work rather
// than silent data loss. Output is deterministic, and Verify fails when
// regeneration would change a file or drop an exported name recorded in
// schema/EXPORTED_IDENTIFIERS.lock unless schema/ALLOWED_REMOVALS.md allows it.
//
// The generator emits data, not behaviour: struct tags, registries,
// constraint checks, supportedOS tables, and round-trip tests. Protocol
// semantics Apple describes only in prose stay in the hand-written
// packages (mdm, ddm, profile). cmd/admgen is the command-line front end.
//
// # References
//
//   - Decision record 0001: docs/research/decisions/0001-architecture.md
//   - Decision record 0003: docs/research/decisions/0003-schema-generator.md
//   - Decision record 0018: docs/research/decisions/0018-go-1.27-baseline.md (JSON policy for generated marshal methods)
//   - Plan of record: docs/research/implementation_plan.md (section 2, the generator; phase 1)
//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md
//   - Apple: https://github.com/apple/device-management
//   - Schema: third_party/device-management/docs/schema.yaml (meta-schema)
//   - Schema: third_party/device-management/mdm/**, declarative/**, other/**
//   - GeneratedFrom: schema/GENERATED_FROM.json, schema/EXPORTED_IDENTIFIERS.lock, schema/ALLOWED_REMOVALS.md
package schemagen
