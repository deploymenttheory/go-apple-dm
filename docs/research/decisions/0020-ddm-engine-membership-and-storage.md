# 0020: DDM engine, membership, and storage

## Context

Declarative device management serves per-enrollment membership and must return the version advertised in the device's manifest.

## Decision

The engine stores declarations, sets, direct assignments, versioned snapshots, status and pending changes through a transactional `Store`. Membership combines assignments with optional `Resolver` and `Expander` hooks. Resolver errors fail the request; expanded bytes determine both the advertised and served token.

`Tokens` and `DeclarationItems` refresh the enrollment snapshot. Declaration fetches read that snapshot's version; unknown identifiers or wrong kinds return 404. All four declaration arrays are present. Upload validates known types and generated structure. Deletes update references and record affected enrollments inside the transaction.

`server/ddmsync.ServiceHook.Complete` clears assignments, snapshots, status and
pending changes on initial or changed-identity `Authenticate` and on `CheckOut`.
Device lifecycle events include dependent user channels; user `CheckOut` clears
only that user. Completion errors fail the check-in before a successful response.
Same-certificate retries and controlled profile replacement preserve that state.

Built-in memory and SQL enrollment stores report the reset or retry selected under
their write lock through the optional `AuthenticateChange.Result` sink. The core
passes this outcome to completion hooks, so a delayed duplicate request cannot
clear assignments created after the winning reset. A successful retry still emits
the existing `Reenrolled` audit occurrence, without claiming another initial
enrollment or certificate rotation.

## Rationale

Versioned bytes prevent an upload between manifest and fetch from returning mismatched content. Transactional change rows make notification work persistent. Per-channel assignments keep device and user configuration independent.

## Constraints

The DDM SQL schema has no foreign key to the MDM enrollment tables so the engine
can run separately. The reference composition shares a SQL pool and unit of work
across the MDM mutation, DDM cleanup and captured events. Cleanup failure rolls
that operation back, including earlier child-channel clears. A successful
per-store authentication result does not assert that the outer transaction
committed.

Memory and independently committing stores have no cross-store rollback. A custom
enrollment store may leave the optional result unknown; the core then uses its
earlier enrollment read for compatibility. That fallback requires external
serialization for concurrent check-ins and does not establish atomic cleanup or
durable retry recovery across separate stores. Platform-superset reference
validation is not implemented.

## Verification

`ddmtest` runs membership, rollback, cascade, snapshot, pagination and lifecycle suites across backends. Engine tests cover hook failures, expanded content, required arrays and 404 behavior.

[Lifecycle integration tests](../../../server/ddmsync/lifecycle_integration_test.go)
cover delayed duplicate authentication, lookup failures, legacy-store fallback,
and shared-SQL rollback and retry after child or parent cleanup failure. The
[authentication outcome contracts](../../../devicemanagement/storage/storagetest/authenticate_result.go)
run against memory and SQL backends, including encrypted wrappers.

## References

- [mdmprotocol/ddm](../../../devicemanagement/mdmprotocol/ddm)
- [storage/ddm](../../../devicemanagement/storage/ddm)
- [server/ddmstore](../../../server/ddmstore)
- [server/ddmsync](../../../server/ddmsync)
- [Authentication outcome contract](../../../devicemanagement/storage/authenticate.go)
- [Shared SQL unit of work](../../../server/sqlstore/sqlcommon/unitofwork.go)
- <https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest>
- <https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations>
- <https://developer.apple.com/documentation/devicemanagement/leveraging-the-declarative-management-data-model-to-scale-devices>
- <https://developer.apple.com/documentation/devicemanagement/check-in>

Reference source identifiers and paths (relative to the named project):

- `jessepeterson/kmfddm@4b75a76`, `README.md`, `storage/storage.go`, `storage/kv/sets.go`, `storage/mysql/sets.go`, `storage/mysql/schema.sql`, `http/api/sets.go`, `http/api/enrollments.go`
- `micromdm/nanohub@3d73c1a`, `ddmadapter/ddmadapter.go`, `SetsRemover`
- `fleetdm/fleet@b44343c`, `server/datastore/mysql/apple_mdm.go`, `resync`, `server/service/apple_mdm.go`, `docs/Contributing/architecture/mdm/apple-declarative-device-management.md`
- `zentralopensource/zentral@b10dd22`, `zentral/contrib/mdm/declarations/protocol.py`, `zentral/contrib/mdm/declarations/linkers.py`, `zentral/contrib/mdm/models.py`, `declaration_items_snapshot`
