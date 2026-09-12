# 0024: Simulator DDM client and predicate subset

## Context

Tests need to observe declarative synchronization and activation outcomes without relying on physical devices.

## Decision

The simulator maintains per-channel declaration state and runs a bounded token/manifest/fetch loop. Unchanged tokens avoid fetches; missing manifest entries and 404 responses remove declarations. A nonconverging server returns `ErrDDMNotSettled`. Fault options cover stale tokens, fetch failures and dropped reports.

The public predicate package implements a documented NSPredicate subset. Upload validates activation syntax; the simulator evaluates it and generates Apple's declaration status reasons. Full and incremental status reports reflect the simulated state.

## Rationale

A bounded, synchronous `Connect` path makes test outcomes deterministic. Shared syntax validation catches unsupported expressions before delivery, while evaluation remains a device-side responsibility.

## Constraints

This is a subset, not a complete NSPredicate implementation. Forms such as `SELF`, `MATCHES`, `LIKE`, `BETWEEN`, `SUBQUERY`, variables and general functions return `ErrUnsupported`. Simulator acceptance does not establish physical-device compatibility.

## Verification

Predicate parse/evaluation tables and fuzz targets cover accepted and rejected syntax. Simulator tests cover convergence, changed-only fetches, removals, status reasons and channel isolation; end-to-end scenarios exercise upload rejection and synchronization.

## References

- [simulator](../../../devicemanagement/simulator)
- [mdmprotocol/ddm/predicate](../../../devicemanagement/mdmprotocol/ddm/predicate)
- <https://developer.apple.com/documentation/devicemanagement/leveraging-the-declarative-management-data-model-to-scale-devices>
- <https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest>
- <https://developer.apple.com/documentation/devicemanagement/status-items>

Reference source identifiers and paths (relative to the named project):

- `fleetdm/fleet@b44343c`, `cmd/osquery-perf/ddm.go`, `server/service/apple_mdm.go`, `docs/Contributing/architecture/mdm/apple-declarative-device-management.md`
- `jessepeterson/kmfddm@4b75a76`, `ddm/declaration.go`
- `zentralopensource/zentral@b10dd22`, `zentral/contrib/mdm/declarations/protocol.py`
