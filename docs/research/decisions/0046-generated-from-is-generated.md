# 0046: Generated schema provenance

## Context

Generated code must identify the schema bytes and revision that produced it, reproducibly across machines and regeneration runs.

## Decision

`GENERATED_FROM.json` is generated with the Go output. Real callers derive the commit from the schema checkout; an explicit commit option supports synthetic test trees. Provenance records the commit's own date, the highest introduced version per OS family, and a SHA-256 digest over sorted YAML paths and contents.

## Rationale

Deriving all fields from source inputs makes verification deterministic. Including paths in the schema digest distinguishes renamed schema files while making the checkout's absolute location irrelevant. OS versions describe the modeled schema surface in addition to the technical commit pin.

## Constraints

The highest introduced version is schema metadata, not a promise of comprehensive OS support. The provenance file must not be edited by hand. Runtime generation time is excluded because it would change otherwise identical output.

## Verification

Generator tests check checkout-derived provenance, path/content/location sensitivity, numeric version ordering and unavailable OS entries. `make verify` compares the provenance file alongside generated Go and the identifier lock.

## References

- [internal/schemagen/generatedfrom.go](../../../internal/schemagen/generatedfrom.go)
- [cmd/admgen](../../../cmd/admgen)
- [schema/GENERATED_FROM.json](../../../schema/GENERATED_FROM.json)
- <https://developer.apple.com/documentation/devicemanagement>

Reference source identifiers and paths (relative to the named project):

- `korylprince/go-adm`
- `deploymenttheory/go-sdk-appleservices`
- `micromdm/nanomdm`
