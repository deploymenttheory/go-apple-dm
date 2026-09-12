// Package schemagen generates schema types, registries, validators, support
// metadata and conformance tests from Apple's YAML.
//
// # Design
//
// A strict loader builds an intermediate model from the pinned device-management
// checkout. Emitters produce deterministic Go and provenance. Verify compares
// regenerated output and rejects exported-name removals unless the removal
// allowlist permits them.
// Audit compares raw YAML independently of strict decoding so unsupported
// metadata does not hide protocol changes. CompareAPI checks generated types,
// signatures and wire tags. BoundaryProbes derives changed support cases from
// source data for comparison with compiled candidate tables.
//
// Apple's descriptions are retained verbatim; project-authored generated
// documentation is maintained in the emitters. Protocol behavior described
// outside the schema belongs to hand-written protocol packages. cmd/schemagen
// provides the command-line interface.
//
// # References
//
//   - Decision record 0001: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0001-architecture.md
//   - Decision record 0046: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0046-generated-from-is-generated.md
//   - Decision record 0003: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0003-schema-generator.md
//   - Decision record 0018: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0018-go-1.27-baseline.md (JSON policy for generated marshal methods)
//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md
//   - Apple: https://github.com/apple/device-management
//   - Schema: third_party/device-management/docs/schema.yaml (meta-schema)
//   - Schema: third_party/device-management/mdm/**, declarative/**, other/**
//   - GeneratedFrom: devicemanagement/schema/GENERATED_FROM.json, devicemanagement/schema/EXPORTED_IDENTIFIERS.lock, devicemanagement/schema/ALLOWED_REMOVALS.md
package schemagen
