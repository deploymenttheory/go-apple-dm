# 0020: DDM engine, membership, and storage

## Context

Declarative device management serves per-enrollment membership and must return the version advertised in the device's manifest.

## Decision

The engine stores declarations, sets, direct assignments, versioned snapshots, status and pending changes through a transactional `Store`. Membership combines assignments with optional `Resolver` and `Expander` hooks. Resolver errors fail the request; expanded bytes determine both the advertised and served token.

`Tokens` and `DeclarationItems` refresh the enrollment snapshot. Declaration fetches read that snapshot's version; unknown identifiers or wrong kinds return 404. All four declaration arrays are present. Upload validates known types and generated structure. Deletes update references and record affected enrollments inside the transaction.

`server/ddmsync.ServiceHook` clears declarative state on `Authenticate` and `CheckOut`, including dependent user channels for device lifecycle events.

## Rationale

Versioned bytes prevent an upload between manifest and fetch from returning mismatched content. Transactional change rows make notification work persistent. Per-channel assignments keep device and user configuration independent.

## Constraints

The DDM SQL schema has no foreign key to the MDM enrollment tables so the engine can run separately. Lifecycle cleanup links the domains through service hooks and is not a distributed transaction. Platform-superset reference validation is not implemented.

## Verification

`ddmtest` runs membership, rollback, cascade, snapshot, pagination and lifecycle suites across backends. Engine tests cover hook failures, expanded content, required arrays and 404 behavior.

## References

- [mdmprotocol/ddm](../../../devicemanagement/mdmprotocol/ddm)
- [storage/ddm](../../../devicemanagement/storage/ddm)
- [server/ddmstore](../../../server/ddmstore)
- [server/ddmsync](../../../server/ddmsync)
- <https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest>
- <https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations>
- <https://developer.apple.com/documentation/devicemanagement/leveraging-the-declarative-management-data-model-to-scale-devices>
- <https://developer.apple.com/documentation/devicemanagement/check-in>

Reference source identifiers and paths (relative to the named project):

- `jessepeterson/kmfddm@4b75a76`, `README.md`, `storage/storage.go`, `storage/kv/sets.go`, `storage/mysql/sets.go`, `storage/mysql/schema.sql`, `http/api/sets.go`, `http/api/enrollments.go`
- `micromdm/nanohub@3d73c1a`, `ddmadapter/ddmadapter.go`, `SetsRemover`
- `fleetdm/fleet@b44343c`, `server/datastore/mysql/apple_mdm.go`, `resync`, `server/service/apple_mdm.go`, `docs/Contributing/architecture/mdm/apple-declarative-device-management.md`
- `zentralopensource/zentral@b10dd22`, `zentral/contrib/mdm/declarations/protocol.py`, `zentral/contrib/mdm/declarations/linkers.py`, `zentral/contrib/mdm/models.py`, `declaration_items_snapshot`
