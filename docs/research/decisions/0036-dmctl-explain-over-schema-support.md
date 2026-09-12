# 0036: `dmctl explain` over `devicemanagement/schema/support`

## Context

Operators need to inspect generated schema support without a running server or a checked-out YAML submodule.

## Decision

`dmctl explain` resolves Go type names, wire identifiers and dotted support paths through the generated registries and `devicemanagement/schema/support` APIs. Ambiguous identifiers return every match unless explicitly limited. Suggestions rank case-insensitive substring and shared-prefix matches.

Tri-state metadata preserves unspecified values. Missing support data or a missing target OS is rendered as unknown. Output reports the registry title/schema path and the support result reason without inventing per-key descriptions.

## Rationale

Using compiled metadata gives the CLI and command validation the same source of support information. Returning ambiguity and unknown values explicitly avoids selecting an unsupported answer.

## Constraints

The command describes the compiled schema pin, not live Apple documentation or the state of a target server. It does not parse YAML or implement separate `Supports`/`Removed` APIs. Hardware and enrollment facts supplied in a target remain the caller's responsibility.

## Verification

Explanation tests cover all families, ambiguity, suggestions, tri-state rendering, unknown support and verbatim reasons. CLI tests run explanation with an unreachable server to verify offline behavior.

## References

- [server/internal/dmctl/explain](../../../server/internal/dmctl/explain)
- [schema/support](../../../devicemanagement/schema/support)
- <https://developer.apple.com/documentation/devicemanagement/commands-and-queries>
- <https://developer.apple.com/documentation/devicemanagement/profile-specific-payload-keys>
- <https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations>
- <https://developer.apple.com/documentation/devicemanagement/status-items>
- <https://github.com/apple/device-management/blob/release/docs/schema.md>

Reference source identifiers and paths (relative to the named project):

- `schema/support/support.go`, `schema/commands/registry.gen.go`
- `schema/profiles/registry.gen.go`, `schema/ddm/registry.gen.go`, `schema/status/registry.gen.go`
- `macadmins/contour`
- `korylprince/go-adm`, `yamlschema`, `declgen`
- `jessepeterson/mdmcommands`
- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a`, `server/mdm/apple/`
