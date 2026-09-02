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
//	admgen names      print the exported identifiers that would be generated
//
// The -schema and -out flags override the checkout and output directories.
// The Makefile runs it as `make verify` (go run ./cmd/admgen verify) after
// initialising the submodule, and the generate-check CI job runs `make
// generate`, `git diff --exit-code`, and `make verify`. The commit stamped
// into every generated file comes from schema/PROVENANCE.json, falling back
// to the submodule's git HEAD.
//
// # References
//
//   - Decision record 0001: docs/research/decisions/0001-architecture.md
//   - Decision record 0003: docs/research/decisions/0003-schema-generator.md
//   - Plan of record: docs/research/implementation_plan.md (section 2, the generator; phase 1)
//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md
//   - Apple: https://github.com/apple/device-management
//   - Schema: third_party/device-management/docs/schema.yaml (meta-schema)
//   - Provenance: schema/PROVENANCE.json, schema/NAMES.lock, schema/RENAMES.md
package main
