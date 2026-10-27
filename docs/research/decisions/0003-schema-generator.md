# 0003: In-repo schema generator over apple/device-management

## Context

Apple's pinned YAML schema describes multiple protocol families, nested dictionaries, validation rules and platform availability.

## Decision

The in-repository generator uses `gopkg.in/yaml.v3` nodes to emit commands and responses, check-in messages, errors, profiles, declarations, declarative protocol messages, status items and other types. Nested dictionaries receive named types. Support metadata remains data in `devicemanagement/schema/support`; callers use `Check`, `Lookup`, `Families` and `Paths`.

Availability comes from Apple's per-key `supportedOS` metadata. A key's
`introduced`, `deprecated` and `removed` values overlay its enclosing key's
effective support field by field, ending at the payload's block; retained earlier
releases are merged as decision 0052 describes. Apple's release publication history
is not an availability source. In Go, a zero `Deprecated` or `Removed` version
means the feature has no such bound.

Dictionaries that repeat a subkeytype with the same shape share one Go type within a
schema; availability is not part of a type. The support table has an entry for every
key use, resolved from Apple's `supportedOS` blocks along that use's own key path,
including the block of each heterogeneous-array variant. Generated validation passes
the support path of the use being checked to nested values, including each element of
an array or of an array of arrays, so a shared type is checked against each use's
entries. A value reached through a recursive reference, such as an element of a
recursive array, is checked against the use of the key it recurses to.

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

The Apple Device Management Schema updates workflow discovers upcoming release and active
seed heads dynamically, excluding inputs already contained in the published pin or
promoted release. It verifies strict parsing, generation, regeneration consistency
and compilation in isolated workspaces, with the published schema as a control.
Incidents group unsupported constructs across files and snapshots and identify the
relevant generator code and required regression tests. The candidate output uses a
fresh identifier lock; the published API guard remains in ordinary CI. When Apple's
release passes, the workflow proposes it as one reviewed adoption pull request that
moves the pin and regenerates with the production identifier lock. Server
compatibility remains the responsibility of ordinary CI.
See the [schema updates guide](../../device-management-schema-updates.md).

## Rationale

One loader and emitter set keeps type generation, validation, metadata and provenance consistent. The identifier lock makes removals reviewable when the schema changes.

The release in which Apple first published a key does not establish when an OS
introduced it. Across the 23 commits on Apple's `release` branch from
`Release_iOS-15_macOS-12` to `Release-v27.2`, measured over 4,527 current key
paths, first publication would raise the floor of 200 macOS and 153 iOS paths
that Apple documented after the OS introduced them, and lower it for 107 macOS
paths published before the OS shipped them. It carries no information for the
2,075 macOS and 1,342 iOS paths already present in the first release, and Apple
revised 227 `introduced` values after first publishing them. Apple's per-key
metadata states each boundary directly and is corrected in place.

## Constraints

Apple's descriptions are preserved verbatim. Project-authored generated documentation is changed in the generator. The lock guards exported names; it does not guarantee that every change to a type is source compatible.

## Verification

Generator tests cover complete-tree generation, schema coverage, nested types, reason vocabularies and identifier removal. `make verify` compares deterministic regeneration with the committed output.

An availability audit walks every key use of the current schema merged with the
retained releases. It requires the entry that generated validation consults for each
use to equal the support resolved from that use's source keys, and each recursive key
to be available as the key it recurses to.

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
