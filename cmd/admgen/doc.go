// Package main implements the admgen schema-generation command.
//
// # Design
//
// The command delegates loading and emission to internal/schemagen:
//
//	admgen generate     regenerate schema packages
//	admgen verify       compare output and check exported identifiers
//	admgen identifiers  list generated exported names
//	admgen versions     report newest introduced OS versions in the schema
//
// The -schema and -out options select source and output directories. make
// generate and make verify initialize the pinned submodule before running the
// command. Generated provenance derives from the schema checkout's commit, date
// and contents; it does not use the generator's execution time.
//
// # References
//
//   - Decision record 0001: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0001-architecture.md
//   - Decision record 0046: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0046-generated-from-is-generated.md
//   - Decision record 0003: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0003-schema-generator.md
//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md
//   - Apple: https://github.com/apple/device-management
//   - Schema: third_party/device-management/docs/schema.yaml (meta-schema)
//   - GeneratedFrom: schema/GENERATED_FROM.json, schema/EXPORTED_IDENTIFIERS.lock, schema/ALLOWED_REMOVALS.md
package main
