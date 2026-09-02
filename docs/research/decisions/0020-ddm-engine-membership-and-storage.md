# 0020: DDM engine, membership, and storage

Status: accepted
Date: 2026-09-02
Phase: 5

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest>
- Doc: <https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations>
- Doc: <https://developer.apple.com/documentation/devicemanagement/leveraging-the-declarative-management-data-model-to-scale-devices>
- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> (`CheckOut`, `Authenticate`)
- YAML: `third_party/device-management/mdm/checkin/declarativemanagement.yaml` (endpoints; an unknown declaration must be 404 and the device removes it; `EnrollmentID` n/a on tvOS and watchOS)
- YAML: `third_party/device-management/declarative/protocol/declarationitemsresponse.yaml` (all four `Declarations` arrays `presence: required`)
- YAML: `third_party/device-management/declarative/declarations/declarationbase.yaml`
- YAML: `third_party/device-management/declarative/declarations/activations/simple.yaml` (a failing configuration does not block siblings)

## References read

- `jessepeterson/kmfddm@4b75a76` `README.md`, `storage/storage.go`, `storage/kv/sets.go`, `storage/mysql/sets.go`, `storage/mysql/schema.sql`, `http/api/sets.go`, `http/api/enrollments.go`
- `micromdm/nanohub@3d73c1a` `ddmadapter/ddmadapter.go` (`SetsRemover` on Authenticate)
- `fleetdm/fleet@b44343c` `server/datastore/mysql/apple_mdm.go` (`resync` rows, host declaration reconcile, cursor batching), `server/service/apple_mdm.go`, `docs/Contributing/architecture/mdm/apple-declarative-device-management.md`
- `zentralopensource/zentral@b10dd22` `zentral/contrib/mdm/declarations/protocol.py`, `zentral/contrib/mdm/declarations/linkers.py`, `zentral/contrib/mdm/models.py` (`declaration_items_snapshot`)

## Known pitfalls found

- KMFDDM #41 (README): set associations are forever; unenrollment leaves the enrollment's sets and status in place, and a re-enrolled device inherits them.
- KMFDDM: no user channel model; every enrollment id is one flat scope with no relation to its device.
- KMFDDM: deleting a declaration that is still in a set succeeds on the KV backend and fails on MySQL (foreign key), so behaviour differs by backend.
- KMFDDM: a set can reference a declaration that no longer exists and the items response is built with the hole in it.
- NanoHUB: the `SetsRemover` hook clears an enrollment's sets on `Authenticate` only; `CheckOut` is not handled.
- Fleet: install versus remove race handled with a `resync` flag; remove rows are only ever `NULL` or pending, so a removed-then-reinstalled declaration needs the flag to be noticed.
- Fleet: flipping a declaration's scope (device to user) leaves the old channel's rows; per-host timestamps for variables are folded into tokens; cursor batching must advance only when the SQL page was full.
- Zentral: references must share a channel and be a platform superset of the referencing declaration; an unmet reference is rejected at upload.
- Zentral: without a per-token snapshot, a re-upload between `declaration-items` and the fetch served a version whose `ServerToken` did not match what the manifest advertised, and the device re-fetched forever.

## What they do

- **KMFDDM**: declarations, sets, enrollment-to-set associations; storage interfaces per concern with KV, file, and MySQL backends; no snapshot; declaration fetch reads the current row.
- **NanoHUB**: wraps KMFDDM storage; clears set associations on Authenticate.
- **Fleet**: MySQL only; declarations scoped to teams and labels; per-host `host_mdm_apple_declarations` rows reconciled by a cron; `resync` flag.
- **Zentral**: artifact versions with platform and channel rules; `declaration_items_snapshot` per enrolled device and user; token captured at command build time and applied on ack.

## What we do better

1. Packages `ddm` (engine), `ddm/inmem`, `ddm/sqlstore`, `ddm/ddmtest`; one `Store` contract proven by `ddmtest.RunAll` on every backend, the same shape as `storagetest`.
2. `ddm/sqlstore` owns its migration set `ddm_schema_migrations` with one `0001_init.sql` per dialect: thirteen tables (`ddm_declarations`, `ddm_declaration_versions`, `ddm_sets`, `ddm_set_declarations`, `ddm_enrollment_sets`, `ddm_enrollment_declarations`, `ddm_snapshots`, `ddm_snapshot_items`, `ddm_status_declarations`, `ddm_status_values`, `ddm_status_errors`, `ddm_status_reports`, `ddm_changes`), all JSON as bytes columns, no JSON column types, no foreign key to `enrollments` so the engine can run in a separate process from the MDM store.
3. `Store.Update(ctx, fn func(Tx) error)` runs every mutation in one transaction and change rows are written inside it, so a failed write leaves no change to notify and a committed write always has one.
4. Membership is sets plus direct assignments plus a `Resolver` hook (dynamic, fails closed on error) plus an `Expander` hook (per-enrollment bytes rewrite, after which the served and advertised token both derive from the expanded bytes); the enrollment id is the scope, device and user channels are distinct, and a set assigned to a device does not apply to its user channel.
5. Each enrollment has a snapshot with declaration versions: `Tokens` and `DeclarationItems` refresh it, `Declaration(kind, identifier)` serves the snapshot's version bytes from `ddm_declaration_versions`, so a re-upload between manifest and fetch still serves what was advertised; unknown identifier or wrong kind is 404, the removal signal Apple specifies.
6. The items response always carries all four arrays, rendered through canonjson from an ordered builder rather than the `omitempty` struct.
7. `ddm.ServiceHook` (a `service.Hook`) clears the enrollment's sets, assignments, snapshot, status, and pending changes on `checkin:CheckOut` and `checkin:Authenticate`, and for a device also its user channels.
8. Deleting a declaration cascades out of sets and snapshots identically on every backend; deleting a set records a change for every affected enrollment; every list is keyset paginated.
9. Unknown types and credential subtypes are rejected at upload with `ErrUnknownType`, and the generated `Validate` runs so a structurally invalid declaration never reaches a device.

## Verified by

1. `inmem.TestContract`, `sqlstore.TestContract`, `sqlstore.TestContractPostgres`, `sqlstore.TestContractMySQL` (all run `ddmtest.RunAll`).
2. `sqlstore.TestMigrationsAgreeAcrossDialects`, `sqlstore.TestOpenMigrates`, `sqlstore.TestRollback`, `sqlstore.TestQueriesFailWithoutSchema` (prove claim 2; would fail on KMFDDM because MySQL is the only SQL backend and the schema is applied by hand).
3. `ddmtest.RunAll/Update/RollbackOnError`, `ddmtest.RunAll/Changes/*` (prove claim 3; would fail on KMFDDM because notify runs after the API answered).
4. `ddmtest.RunAll/Assignments/UnionDedupe`, `/Assignments/SortedRegardlessOfInsertOrder`, `/Assignments/UserChannelIsNotDeviceChannel`, `/Assignments/AffectedEnrollments`, `ddm.TestManifest/ResolverFailsClosed`, `ddm.TestManifest/ExpanderRewritesAndRetokens` (prove claim 4; would fail on KMFDDM because there is no channel model and no dynamic membership).
5. `ddmtest.RunAll/Snapshots/*`, `ddm.TestDeclaration/ServesSnapshotVersionAfterReupload`, `/UnknownIs404`, `/WrongKindIs404` (prove claim 5; would fail on KMFDDM because the fetch reads the live row).
6. `ddm.TestDeclarationItems/AllFourArraysPresent` (prove claim 6; would fail on a struct with `omitempty`).
7. `ddm.TestServiceHook/CheckOutClears`, `/ReauthenticateClearsUserChannels`, `ddmtest.RunAll/Clear/OnlyThatEnrollment` (prove claim 7; would fail on KMFDDM #41 and on NanoHUB for CheckOut).
8. `ddmtest.RunAll/Declarations/DeleteCascades`, `/Declarations/Pagination`, `/Sets/DeleteRecordsChanges`, `/Declarations/PruneVersions`, `/Declarations/KindChangeConflict` (prove claim 8; would fail on KMFDDM because KV and MySQL disagree).
9. `ddm.TestParseDeclaration/UnknownType`, `/CredentialSubtypeRejected`, `/GeneratedValidateRuns` (prove claim 9; would fail on KMFDDM because any `com.apple.` prefixed type is accepted).

## Rejected alternatives

- Foreign keys from `ddm_*` tables to `enrollments`: the engine must run in the `ddm` role without the MDM schema; the service hook is the lifecycle link instead.
- JSON column types: MySQL normalises JSON and PostgreSQL `jsonb` reorders keys, so the stored bytes would not be the hashed bytes (Fleet's regression).
- Serving the current declaration row on fetch (KMFDDM): the device compares the fetched `ServerToken` with the manifest and re-fetches until they agree.
- A cron reconcile with per-host rows (Fleet): the change rows and notifier (0022) cover the same need without a scan.
- Platform-superset reference checks (Zentral): the engine has no platform model; unmet references are reported by the device through status and stored (0021).
