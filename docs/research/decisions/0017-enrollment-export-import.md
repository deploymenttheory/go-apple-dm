# 0017: Enrollment export and import

Status: accepted
Date: 2026-09-02
Phase: 4

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> (an enrollment is the sum of `Authenticate`, `TokenUpdate`, and `SetBootstrapToken` state; a device never re-sends them unprompted)
- YAML: `third_party/device-management/mdm/checkin/authenticate.yaml`
- YAML: `third_party/device-management/mdm/checkin/tokenupdate.yaml` (`UnlockToken`, `PushMagic`, `Token`)
- YAML: `third_party/device-management/mdm/checkin/setbootstraptoken.yaml`

## References read

- `micromdm/nanomdm@4948319` `storage/migrate.go`, `storage/mysql/migrate.go`, `storage/kv/migrate.go`, `cmd/nano2nano/main.go`, `cmd/nanomdm/main.go` (`/migration`)
- `micromdm/micromdm@904493b` `platform/device/service.go`, `platform/device/get_devices.go`
- `fleetdm/fleet@b44343c` `tools/mdm/migration/micromdm/touchless/main.go`, `server/datastore/mysql/migrations/tables/20240702123921_AddEnrolledFromMigrationColumn.go`
- `zentralopensource/zentral@b10dd22` `zentral/contrib/mdm/models.py` (`EnrolledDevice.blocked_at`, escrow fields), `zentral/contrib/mdm/public_views/mdm.py`
- NanoMDM #86

## Known pitfalls found

- NanoMDM #86: replaying stored `Authenticate` and `TokenUpdate` messages loses bootstrap tokens, unlock tokens not embedded in the last `TokenUpdate`, certificate associations, and the enabled flag, so disabled devices are resurrected on the target.
- NanoMDM: `nano2nano` exits 0 on a partial failure, and an `Authenticate` without a following `TokenUpdate` leaves the target enrollment disabled.
- MicroMDM: none of the needed state (unlock token, bootstrap token, identity) is reachable through its API.
- Fleet: the only record import is a script that emits raw SQL, pins the old identity, and marks `enrolled_from_migration`.
- Zentral: no export; re-enrollment leaves old escrowed secrets in place and clears `blocked_at`.

## What they do

- **NanoMDM**: `RetrieveMigrationCheckins` streams the raw `Authenticate` and `TokenUpdate` plists; `nano2nano` posts each to the target's `/migration` endpoint, which bypasses certauth; `TokenUpdateTally` decides whether an enrollment counts as complete.
- **MicroMDM**: `GetDevices` returns inventory fields only; secrets live in BoltDB buckets.
- **Fleet**: `touchless/main.go` reads a MicroMDM export and prints `INSERT` statements for `nano_devices`, `nano_enrollments`, and `nano_cert_auth_associations`; a column flags migrated rows.
- **Zentral**: `EnrolledDevice` and `EnrolledUser` rows are only created by check-in; the admin can block a device but not move it.

## What we do better

1. `MigrationStore.Export` yields typed `EnrollmentExport` values with every enrollment field, both raw plists, the bootstrap token, the unlock token, the enabled flag, the timestamps, and the certificate history; rows are ordered by `(parent_id, id)` so a device always precedes its users, with a keyset cursor of `parent_id`, a NUL byte, and `id` (a cursor without the NUL is `ErrInvalid`).
2. `Import` is one transaction that upserts by id and preserves `Enabled`, the timestamps, the pin, and the tokens. It never touches the command queue, so an import cannot resurrect a disabled device or drop queued work.
3. Invalid input is refused before any write: an orphan user channel or history rows for another id fail with `ErrInvalid`, a malformed id fails with `ErrInvalid`, and a `CertHash` already pinned to another enrollment fails with `ErrConflict`.
4. Sealed columns are re-sealed under the target keyring, so an export from a plaintext store imports into an encrypted one and back without loss.
5. Import is idempotent and paginated; `Core.ExportEnrollments` and `Core.ImportEnrollment` run the hook chain with `Call.Op` `"export"` and `"import"`, map storage errors through `codeForStorage`, and `ImportEnrollment` publishes `EnrollmentImported` with actor `admin`.
6. `token_update_raw` is now retained beside `authenticate_raw` (`Enrollment.TokenUpdateRaw`, written through the `raw` parameter of `StoreTokenUpdate`), so a NanoMDM-style replay converter can be built under `cmd/` later without a schema change.

Push certificates move through 0015 (`PushCert` returns the key for an admin export). User authentication state (0016) is session scoped and is not exported.

## Verified by

1. `storagetest.RunMigrationSuite/RoundTripAllFields`, `RunMigrationSuite/OrderParentsFirstAndPagination` (device before user at page sizes 1 and 2, no duplicates, bad cursor is `ErrInvalid`) (prove claim 1; would fail on NanoMDM because the replay carries neither the bootstrap token nor the cert association).
2. `storagetest.RunMigrationSuite/RoundTripAllFields` (`Enabled` false survives; queue untouched), `RunMigrationSuite/ImportRejects` (re-importing the same record does not duplicate history) (prove claim 2; would fail on NanoMDM because a replayed `Authenticate` enables the target and clears its queue).
3. `storagetest.RunMigrationSuite/ImportRejects` (orphan user channel, empty id, history rows for another id, and a user channel carrying device state are `ErrInvalid`; a `CertHash` pinned elsewhere is `ErrConflict`) (prove claim 3; would fail on Fleet because the generated SQL pins the identity unconditionally).
4. `sqlite.TestCrossBackendMigration` (inmem to encrypted SQLite to inmem, deep equal) (proves claim 4).
5. `storagetest.RunMigrationSuite/OrderParentsFirstAndPagination`, `RunMigrationSuite/ImportRejects` (idempotent re-import), `service.TestImportExportPublishes` (hooks run, `EnrollmentImported` published, `ErrInvalid` surfaces as `CodeBadRequest`) (prove claim 5; would fail on `nano2nano` because a partial failure is not reported).
6. `storagetest.RunMigrationSuite/RoundTripAllFields` (`TokenUpdateRaw` round-trips), `service.TestStorageFailuresAreInternal` (`Export` and `Import` failures through `storagetest.Failing` are `CodeInternal`) (prove claim 6).

## Rejected alternatives

- Replaying raw check-in messages as the transport (NanoMDM): loses every field that is not in the last message and bypasses identity checks on the target.
- A SQL dump: not portable across the four backends and not re-sealable under a different keyring.
- Exporting `user_auth` rows: challenges and tokens belong to a session on the source server.
- Exporting push certificates inside `EnrollmentExport`: a certificate is per topic, not per enrollment, and 0015 already exposes it.
