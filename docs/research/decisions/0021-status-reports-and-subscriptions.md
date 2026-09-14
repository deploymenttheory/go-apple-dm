# 0021: Status reports and status subscriptions

## Context

Status reports contain declaration outcomes, inventory values and errors, with full reports replacing state and partial reports updating it.

## Decision

The engine bounds and strictly decodes status JSON. Registry paths identify known status items; unknown values, arrays and nulls remain stored as canonical bytes. Declaration outcomes are keyed by kind and identifier, preferring the snapshot token when duplicates appear. Raw reports have configurable retention, and report errors are retained separately.

A full report atomically removes absent status. Optional synthesized subscriptions request reported capabilities, filtered by configured exclusions, or a baseline when capabilities are unknown. A synthesized unconditional activation references the generated subscription so devices process it. An explicitly assigned subscription takes precedence and owns its activation; an explicitly assigned companion activation also takes precedence. Both generated declarations participate in snapshots and token comparison. Successful status submission returns 200 with an empty body.

The engine exposes `StatusValues` with prefix filtering/pagination, `StatusErrors`
and reverse-chronological `StatusReports`. Admin routes currently expose only part
of this query surface. Values are observations, not a normalized fleet inventory
or compliance result. `Manifest`, `Tokens` and `DeclarationItems` refresh persisted
snapshots; callers must not assume these delivery methods are read-only previews.

## Rationale

Preserving typed item boundaries and canonical values avoids data loss from flattening. Snapshot-aware outcome selection keeps status tied to served content. Capability-derived subscriptions converge through the same token mechanism as other declarations.

## Constraints

The default report limit is 1 MiB. Capability parsing is defensive and yields an empty set for missing or malformed subkeys. An absent item in a partial report is not evidence of removal. A present `management.declarations` item replaces its complete collection even when `FullReport` is false or omitted; declaration entries absent from that collection are removed. This keeps the parsed declaration rows consistent with the stored item. The September 13, 2026 Mac test demonstrated removal through such a partial report.

## Verification

Status tests cover limits, strict JSON errors, duplicate identifiers, full/partial reports, errors, unknown paths and event publication. Store suites cover arrays/nulls and retention; subscription tests cover convergence, exclusions and explicit overrides.

## References

- [mdmprotocol/ddm/status_query.go](../../../devicemanagement/mdmprotocol/ddm/status_query.go)
- [mdmprotocol/ddm/status.go](../../../devicemanagement/mdmprotocol/ddm/status.go)
- [mdmprotocol/ddm/subscriptions.go](../../../devicemanagement/mdmprotocol/ddm/subscriptions.go)
- [storage/ddm](../../../devicemanagement/storage/ddm)
- <https://developer.apple.com/documentation/devicemanagement/status-items>
- <https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest>
- [Apple's management.declarations schema](https://github.com/apple/device-management/blob/release/declarative/status/management.declarations.yaml)

Reference source identifiers and paths (relative to the named project):

- `jessepeterson/kmfddm@4b75a76`, `ddm/status.go`, `ddm/path.go`, `storage/mysql/status.go`, `storage/mysql/schema.sql`, `storage/kv/status.go`, `http/ddm/ddm.go`, `http/api/status.go`
- `fleetdm/fleet@b44343c`, `server/service/apple_mdm.go`, `server/datastore/mysql/apple_mdm.go`, `MDMAppleStoreDDMStatusReport`
- `zentralopensource/zentral@b10dd22`, `zentral/contrib/mdm/declarations/status_report.py`, `zentral/contrib/mdm/declarations/management.py`, `build_target_management_status_subscriptions`
