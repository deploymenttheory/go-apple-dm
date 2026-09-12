// Package main implements the schemagen schema-generation command.
//
// # Design
//
// The command delegates loading and emission to internal/schemagen:
//
//	schemagen generate     regenerate schema packages
//	schemagen verify       compare output and check exported identifiers
//	schemagen identifiers  list generated exported names
//	schemagen versions     report newest introduced OS versions in the schema
//	schemagen audit        compare raw schemas and collect strict parsing failures
//	schemagen api-diff     compare generated declarations and wire tags
//	schemagen boundaries   derive changed support cases from source schemas
//
// The -schema and -out options select source and output directories. make
// generate and make verify initialize the pinned submodule before running the
// command. Generated provenance derives from the schema checkout's commit, date
// and contents; it does not use the generator's execution time.
// The -ref option selects provenance explicitly, otherwise the command reads
// the submodule's configured branch. Audit and boundaries take a -baseline
// source tree; api-diff takes baseline and candidate generated directories.
// The compatibility monitor uses immutable checkouts and invokes the command
// directly, because make's submodule prerequisite restores the committed pin.
//
// # References
//
//   - Decision record 0001: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0001-architecture.md
//   - Decision record 0046: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0046-generated-from-is-generated.md
//   - Decision record 0003: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0003-schema-generator.md
//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md
//   - Apple: https://github.com/apple/device-management
//   - Schema: third_party/device-management/docs/schema.yaml (meta-schema)
//   - GeneratedFrom: devicemanagement/schema/GENERATED_FROM.json, devicemanagement/schema/EXPORTED_IDENTIFIERS.lock, devicemanagement/schema/ALLOWED_REMOVALS.md
package main
