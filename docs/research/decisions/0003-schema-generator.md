# 0003: In-repo schema generator over apple/device-management

Status: accepted
Date: 2026-09-01
Phase: 1

## Apple sources

- Doc: <https://github.com/apple/device-management/blob/release/docs/schema.md>
- YAML: `third_party/device-management/docs/schema.yaml` (meta-schema), `mdm/**`, `declarative/**`, `other/**`

## References read

- `jessepeterson/admgen` (`admgencmd`), `jessepeterson/mdmcommands` (generated output, `generate.go`)
- `korylprince/go-adm` (`yamlschema`, `cmdgen`, `profilegen`, `declgen`, `GENERATE_SHA`)
- `deploymenttheory/go-sdk-appleservices` `device_management/internal/{spec,codegen}`, `cmd/fetchspec`, `PROVENANCE.json`
- `macadmins/contour` `crates/mdm-schema` (Rust validator)

## Known pitfalls found

- admgen/mdmcommands generate commands only; no responses beyond what Apple lists, no check-in, no DDM.
- go-adm needs a forked go-yaml to parse Apple's recursive YAML.
- go-sdk-appleservices flattens `supportedOS` into comments and regenerates weekly with no rename protection; 31 of 66 commands have no response struct.
- Apple's schema uses in-file YAML anchors (`&id001` / `*id001`) for shared `subkeytype` structures; every `subkeytype` is paired with `subkeys` in the same file (verified by grep on the pinned commit).

## What they do

- **admgen**: one struct per command from `payloadkeys`, response from `responsekeys`; plist tags; no validation.
- **go-adm**: AST over the YAML, emits commands, profiles, declarations; pins a commit.
- **go-sdk-appleservices**: JSON snapshots of the YAML, generated `Validate()` with literals baked in, deterministic regeneration gate in CI.

## What we do better

1. Every family generated: commands with a response type for all 65, check-in, errors, profiles, declarations, DDM protocol, status items, and `other`.
2. `supportedOS` is kept as data: a `schema/support` package answers introduced, deprecated, removed, channel, supervision, shared iPad, and user enrollment questions at runtime, and `Validate(version)` uses it.
3. Naming contract with `schema/NAMES.lock` and `RENAMES.md`; `admgen verify` fails on silent removals.
4. Nested dictionaries become named types; `subkeytype` shared shapes are emitted once per file.
5. Generated conformance tests round-trip every type through XML plist, binary plist, and JSON with every documented key populated, and assert registry coverage of every YAML file.
6. `gopkg.in/yaml.v3` node parsing with our own recursion; no forked YAML library.

## Verified by

1. `TestRegistryCoversEveryYAML` per family.
2. `TestSupportsMatrix` golden table; `TestValidateVersionAware`.
3. `TestRenameGuard` (removes an identifier in a temp copy and expects failure).
4. `TestSubkeyTypeEmittedOnce`.
5. `Test*Conformance` generated files.
6. `TestLoadAllYAMLNoUnknownKeys` (strict loader fails on unknown keys).

## Rejected alternatives

- External generator dependency (go-adm, admgen): no naming contract, partial coverage.
- Hand-written types: 300+ YAML files change yearly; unsustainable.
