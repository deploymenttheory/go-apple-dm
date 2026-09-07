# 0017: Enrollment export and import

## Context

Moving enrollment state between backends requires fields that are not all present in the last check-in messages.

## Decision

`MigrationStore.Export` pages typed `EnrollmentExport` values, including enrollment fields, raw check-ins, escrowed tokens, timestamps, enabled state and certificate history. Ordering by parent and identifier places devices before users. Each import transaction validates and upserts one enrollment while preserving its state and leaving the command queue unchanged. Target stores seal imported secrets with their own keyring.

Service export/import methods execute hooks, map storage errors and publish `EnrollmentImported` after a successful import.

## Rationale

Typed records preserve state across backends without replaying protocol messages or copying driver-specific SQL. Per-record transactions make validation and retries explicit.

## Constraints

Exports contain plaintext secrets and require protected transport and storage. Push certificates move through their own store APIs. User-authentication sessions, command queues, account-driven associations and revocation state are not part of `EnrollmentExport`; coordinate their migration separately. A multi-record import is not one atomic transaction.

## Verification

Migration suites cover complete round trips, parent-first pagination, disabled enrollments, preserved queues, idempotency and rejected conflicts. SQL tests move records between in-memory and encrypted SQLite stores.

## References

- [storage](../../../storage)
- [storage/storagetest](../../../storage/storagetest)
- [server/service/migrate.go](../../../server/service/migrate.go)
- [server/sqlstore/sqlite](../../../server/sqlstore/sqlite)
- <https://developer.apple.com/documentation/devicemanagement/check-in>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm@4948319`, `storage/migrate.go`, `storage/mysql/migrate.go`, `storage/kv/migrate.go`, `cmd/nano2nano/main.go`, `cmd/nanomdm/main.go`, `/migration`
- `micromdm/micromdm@904493b`, `platform/device/service.go`, `platform/device/get_devices.go`
- `fleetdm/fleet@b44343c`, `tools/mdm/migration/micromdm/touchless/main.go`, `server/datastore/mysql/migrations/tables/20240702123921_AddEnrolledFromMigrationColumn.go`
- `zentralopensource/zentral@b10dd22`, `zentral/contrib/mdm/models.py`, `EnrolledDevice.blocked_at`, `zentral/contrib/mdm/public_views/mdm.py`
