# 0041: Apple's closed vocabularies as Go constants

## Context

Apple declares reason-code vocabularies separately from payload fields. The same code can have different descriptions and details in different status schemas.

## Decision

The generator emits reason constants and `Reasons` tables for families whose YAML declares `reasons`. Entries retain their source schema, so multiple meanings of the same code survive. `ReasonCodes` returns a sorted vocabulary, and generated conformance tests relate constants to table entries. Exported names are covered by the identifier lock.

APNs reason constants and associated status codes are maintained in the APNs package from Apple's response documentation. Push outcome classification remains a separate concern (record 0042).

## Rationale

Schema-scoped entries preserve Apple's meanings without flattening them into a single description. Generated tables also provide a bounded vocabulary for callers such as telemetry.

## Constraints

Generated descriptions remain Apple's text. No inferred `IsError` or `IsInfo` semantics are generated from name prefixes. APNs reasons are maintained from documentation, not generated from the pinned YAML. Unknown reason strings can still arrive on the wire.

## Verification

Generator tests cover absent vocabularies, shared codes with distinct meanings and naming-lock entries. Generated tests check table completeness and ordering. APNs tests independently pin documented strings and status pairs.

## References

- [internal/schemagen/emit_reasons.go](../../../internal/schemagen/emit_reasons.go)
- [schema/ddm](../../../devicemanagement/schema/ddm)
- [schema/status](../../../devicemanagement/schema/status)
- [appleplatformservices/push/apns](../../../devicemanagement/appleplatformservices/push/apns)
- <https://developer.apple.com/documentation/devicemanagement/statusreport>
- <https://developer.apple.com/documentation/devicemanagement/status-items>
- <https://developer.apple.com/documentation/usernotifications/handling-notification-responses-from-apns>

Reference source identifiers and paths (relative to the named project):

- `korylprince/go-adm@7a87c98afb418bebb6c3f94b9edacf634ce55a2c`, `schema/schema.gen.go:124,287-295`
- `schema/schema.go:91-99`, `generated/declarative/status/status.go:373-385,445-452`
- `jessepeterson/admgen@b26a7609b0a326988e3cea8853de5ee0061f8fab`, `cmd/admgencmd/builder.go:41-45,520-528`
- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a`, `server/fleet/apple_mdm.go:1416,1436-1467`
- `server/service/apple_mdm.go:8046-8172`, `server/mdm/apple/util.go:174-180`
- `server/mdm/apple/apns_errors.go`, `server/mdm/apple/apple_mdm.go:1843-1879`
- `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151`, `ddm/status.go:21-28,50-54,82-85,95-102`
- `storage/mysql/status.go:37,253-254`
- `zentralopensource/zentral@6b93d01d1bc8471ed98807b02a26b83452e8c8b7`
- `zentral/contrib/mdm/declarations/linkers.py:33-51`, `declarations/status_report.py:15-32`
- `zentral/contrib/mdm/apns.py:59-77`, `schema_data/declarative/declarations/declarationbase.yaml`
- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10`, `push/nanopush/provider.go:41-66`
- `push/push.go:12-15`, `mdm/checkin.go:96-104`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107`, `platform/apns/service.go:97-101`
- `platform/apns/push.go:80-83`
- `micromdm/nanohub@3d73c1a83d5a042bfa5d31ba98d32de996007667`, `ddmadapter/ddmadapter.go:10,116-136`
- `jessepeterson/mdmcommands@0ef71b4590d729da33dcb653c5f7d8e6bdce6624`, `generate.go:9`, `cmd_device.go:825-841`
