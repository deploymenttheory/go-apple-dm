// Package main is the admgen command, which regenerates the schema/
// packages from the vendored Apple device management YAML and verifies
// that the checked-in output is current.
//
// # Why
//
// The generated schema packages must track the pinned
// third_party/device-management submodule exactly, and CI must be able to
// prove it. This binary is the thin front end over internal/schemagen that
// phase 1 of the plan of record calls for (decision record 0003):
//
//	admgen generate   regenerate schema/ from third_party/device-management
//	admgen verify     fail if regeneration would change anything or drop a name
//	admgen identifiers  print the exported identifiers that would be generated
//	admgen versions     print the newest OS version per family in the checkout
//
// The -schema and -out flags override the checkout and output directories.
// The Makefile runs it as `make verify` (go run ./cmd/admgen verify) after
// initialising the submodule, and the generate-check CI job runs `make
// generate`, `git diff --exit-code`, and `make verify`.
//
// The commit stamped into every generated file, and into
// schema/GENERATED_FROM.json, is the checkout's git HEAD. It used to be read
// out of GENERATED_FROM.json itself, which meant a submodule bump regenerated
// from Apple's new YAML while every file claimed the old commit, and verify
// agreed because it read the same stale value (decision record 0046).
//
// # References
//
//   - Decision record 0001: docs/research/decisions/0001-architecture.md
//   - Decision record 0046: docs/research/decisions/0046-generated-from-is-generated.md
//   - Decision record 0003: docs/research/decisions/0003-schema-generator.md
//   - Plan of record: docs/research/implementation_plan.md (section 2, the generator; phase 1)
//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md
//   - Apple: https://github.com/apple/device-management
//   - Schema: third_party/device-management/docs/schema.yaml (meta-schema)
//   - GeneratedFrom: schema/GENERATED_FROM.json, schema/EXPORTED_IDENTIFIERS.lock, schema/ALLOWED_REMOVALS.md
package main
