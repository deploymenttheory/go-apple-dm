# 0003: In-repo schema generator over apple/device-management

## Context

Apple's pinned YAML schema describes multiple protocol families, nested dictionaries, validation rules and platform availability.

## Decision

The in-repository generator uses `gopkg.in/yaml.v3` nodes to emit commands and responses, check-in messages, errors, profiles, declarations, declarative protocol messages, status items and other types. Nested dictionaries receive named types. Support metadata remains data in `devicemanagement/schema/support`; callers use `Check`, `Lookup`, `Families` and `Paths`.

Generated conformance tests exercise XML plist, binary plist and JSON round trips. `devicemanagement/schema/EXPORTED_IDENTIFIERS.lock` tracks exported names, and approved removals belong in `devicemanagement/schema/ALLOWED_REMOVALS.md`. Provenance is generated from the checked-out schema as described in record 0046.

## Rationale

One loader and emitter set keeps type generation, validation, metadata and provenance consistent. The identifier lock makes removals reviewable when the schema changes.

## Constraints

Apple's descriptions are preserved verbatim. Project-authored generated documentation is changed in the generator. The lock guards exported names; it does not guarantee that every change to a type is source compatible.

## Verification

Generator tests cover complete-tree generation, schema coverage, nested types, reason vocabularies and identifier removal. `make verify` compares deterministic regeneration with the committed output.

## References

- [internal/schemagen](../../../internal/schemagen)
- [cmd/admgen](../../../cmd/admgen)
- [schema](../../../devicemanagement/schema)
- <https://github.com/apple/device-management/blob/release/docs/schema.md>

Reference source identifiers and paths (relative to the named project):

- `jessepeterson/admgen`, `admgencmd`, `jessepeterson/mdmcommands`, `generate.go`
- `korylprince/go-adm`, `yamlschema`, `cmdgen`, `profilegen`, `declgen`, `GENERATE_SHA`
- `deploymenttheory/go-sdk-appleservices`, `device_management/internal/{spec,codegen}`, `cmd/fetchspec`, `GENERATED_FROM.json`
- `macadmins/contour`, `crates/mdm-schema`
