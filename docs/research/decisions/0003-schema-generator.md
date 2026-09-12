# 0003: In-repo schema generator over apple/device-management

## Context

Apple's pinned YAML schema describes multiple protocol families, nested dictionaries, validation rules and platform availability.

## Decision

The in-repository generator uses `gopkg.in/yaml.v3` nodes to emit commands and responses, check-in messages, errors, profiles, declarations, declarative protocol messages, status items and other types. Nested dictionaries receive named types. Support metadata remains data in `devicemanagement/schema/support`; callers use `Check`, `Lookup`, `Families` and `Paths`.

Generated conformance tests exercise XML plist, binary plist and JSON round trips. `devicemanagement/schema/EXPORTED_IDENTIFIERS.lock` tracks exported names, and approved removals belong in `devicemanagement/schema/ALLOWED_REMOVALS.md`. Provenance is generated from the checked-out schema as described in record 0046.

The schema monitor follows Apple's advertised stable default branch and discovers
every branch whose name starts with `seed`. It compares the project pin to stable,
and stable to each seed, using recorded commit SHAs. Raw YAML comparison and strict
parsing are independent; parser failures do not suppress engineering review evidence.
Complete generated changes can produce a stable update PR or a draft seed preview.
Neither the removal allowance nor the server dependency requirement is edited by
automation. Engineering issues distinguish executable failures from behavior reviews;
blocked checks cannot establish a fix. See the [monitor guide](../../schema-monitor.md).

## Rationale

One loader and emitter set keeps type generation, validation, metadata and provenance consistent. The identifier lock makes removals reviewable when the schema changes.

## Constraints

Apple's descriptions are preserved verbatim. Project-authored generated documentation is changed in the generator. The lock guards exported names; it does not guarantee that every change to a type is source compatible.

## Verification

Generator tests cover complete-tree generation, schema coverage, nested types, reason vocabularies and identifier removal. `make verify` compares deterministic regeneration with the committed output.

## References

- [internal/schemagen](../../../internal/schemagen)
- [cmd/schemagen](../../../cmd/schemagen)
- [schema](../../../devicemanagement/schema)
- <https://github.com/apple/device-management/blob/release/docs/schema.md>

Reference source identifiers and paths (relative to the named project):

- `jessepeterson/admgen`, `admgencmd`, `jessepeterson/mdmcommands`, `generate.go`
- `korylprince/go-adm`, `yamlschema`, `cmdgen`, `profilegen`, `declgen`, `GENERATE_SHA`
- `deploymenttheory/go-sdk-appleservices`, `device_management/internal/{spec,codegen}`, `cmd/fetchspec`, `GENERATED_FROM.json`
- `macadmins/contour`, `crates/mdm-schema`
