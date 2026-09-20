# 0003: In-repo schema generator over apple/device-management

## Context

Apple's pinned YAML schema describes multiple protocol families, nested dictionaries, validation rules and platform availability.

## Decision

The in-repository generator uses `gopkg.in/yaml.v3` nodes to emit commands and responses, check-in messages, errors, profiles, declarations, declarative protocol messages, status items and other types. Nested dictionaries receive named types. Support metadata remains data in `devicemanagement/schema/support`; callers use `Check`, `Lookup`, `Families` and `Paths`.

Version parsing and comparison live in `devicemanagement/osversion`, with named
macOS major constants used by generated availability boundaries. Generator input
parsing and boundary comparisons call this package directly, and support metadata
declares version fields as `osversion.Version`. Reviewed prose
supplements in the generator add value-specific availability metadata and typed
field-relationship checks. `profiles.ValueSupport(path, value)` exposes the extra
SSO floors; direct payload `Validate` calls enforce them along with the generated
key constraints. Supplements preserve inherited support requirements and do not
modify the vendored Apple input.

Generated conformance tests exercise XML plist, binary plist and JSON round trips. `devicemanagement/schema/EXPORTED_IDENTIFIERS.lock` tracks exported names, and approved removals belong in `devicemanagement/schema/ALLOWED_REMOVALS.md`. Provenance is generated from the checked-out schema as described in record 0046.

The Device Management Client Schema monitor discovers upcoming release and active
seed heads dynamically, excluding inputs already contained in the published pin or
promoted release. It verifies strict parsing, generation, regeneration consistency
and compilation in isolated workspaces, with the published schema as a control.
Incidents group unsupported constructs across files and snapshots and identify the
relevant generator code and required regression tests. The candidate output uses a
fresh identifier lock; the published API guard remains in ordinary CI. Source
adoption and server compatibility are outside the monitor's responsibility.
See the [monitor guide](../../schema-monitor.md).

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
