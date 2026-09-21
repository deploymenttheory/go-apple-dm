# 0026: DEP client, device sync, and profile assignment

## Context

The device enrollment service uses account-scoped OAuth 1.0a credentials, rotating sessions and cursors. Device sync and profile assignment must recover from partial failures.

## Decision

`dep.Client` resolves accounts through a store, signs requests, coordinates session refresh per account and retries authentication once. Token PKI handling validates imported `.p7m` contents before updating the active credentials. Account expiry and service errors are typed.

`Syncer` performs fetch then incremental sync and commits cursors with each page. Each full fetch persists a generation and marks returned serials. Only successful completion tombstones active devices absent from that generation, including an empty snapshot. Account locks and cursor revisions reject responses from superseded sync passes. An interrupted fetch resumes without deleting unseen inventory early.

`Assigner` compares stored profile state with desired state and records per-device outcomes. Account retry deadlines and failure counts live in the store, so a new worker respects an earlier Retry-After response. A renewable account claim fences competing workers; bounded network requests run outside transactions, and writes recheck claim ownership. Assignment readback cannot restore a device removed by sync. The reference server schedules sync and assignment independently; zero disables the corresponding background operation. API profiles retain unknown fields and validate documented combinations and setup keys.

DEP migration 0002 adds cursor revision/generation fields, device fetch markers and assignment state. Custom stores must implement `MarkFetched`, `AssignmentState`, `PutAssignmentState` and transactional `LockAccount` with the shared contract semantics. The server module pins a library revision implementing this contract so standalone builds use the same interfaces.

## Rationale

Committing a cursor with its data supports at-least-once page delivery. Assignment based on current state handles re-fetches and server moves as well as newly added devices. Typed errors distinguish operator action from transient retry.

## Constraints

API types are maintained from Apple's documentation because this service's JSON schemas are not in the pinned YAML submodule. `dep` is retained as an API/package identifier; user-facing enrollment terminology is Automated Device Enrollment. Live Apple service behavior requires integration validation.

## Verification

Client, PKI, syncer and assigner tests use the independent fake service for signature verification, session rotation, pagination, injected errors and per-serial outcomes. Store contracts cover resumable and empty snapshots, persisted backoff, concurrent account locking and rollback across memory, SQLite, PostgreSQL and MySQL. Worker race tests cover stale sync responses, expired claims and readback after removal. End-to-end tests cover assignment and ADE enrollment.

## References

- [appleplatformservices/dep](../../../devicemanagement/appleplatformservices/dep)
- [storage/dep](../../../devicemanagement/storage/dep)
- [server/depstore](../../../server/depstore)
- <https://developer.apple.com/documentation/devicemanagement/device-assignment>
- <https://developer.apple.com/documentation/devicemanagement/authenticating-for-automated-device-enrollment>
- <https://developer.apple.com/documentation/devicemanagement/fetch-devices>
- <https://developer.apple.com/documentation/devicemanagement/sync-devices>
- <https://developer.apple.com/documentation/devicemanagement/define-profile>
- <https://developer.apple.com/documentation/devicemanagement/assign-profile>
- <https://developer.apple.com/documentation/devicemanagement/clear-device-profile>
- <https://developer.apple.com/documentation/devicemanagement/fetch-profile>
- <https://developer.apple.com/documentation/devicemanagement/profile>
- <https://developer.apple.com/documentation/devicemanagement/device>
- <https://developer.apple.com/documentation/devicemanagement/fetchdeviceresponse>
- <https://developer.apple.com/documentation/devicemanagement/device-details>
- <https://developer.apple.com/documentation/devicemanagement/disown-devices>
- <https://developer.apple.com/documentation/devicemanagement/activation-lock-devices>
- <https://developer.apple.com/documentation/devicemanagement/get-beta-enrollment-tokens>
- <https://developer.apple.com/documentation/devicemanagement/assign-account-driven-enrollment-profile>
- <https://developer.apple.com/documentation/devicemanagement/fetch-account-driven-enrollment-profile>
- <https://developer.apple.com/documentation/devicemanagement/remove-account-driven-enrollment-profile>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanodep@2223746`, `client/`, `godep/`, `tokenpki/`, `sync/`, `storage/`, `proxy/`, `cmd/depsyncer`, `docs/operations-guide.md`
- `fleetdm/fleet@b44343c`, `server/mdm/apple/apple_mdm.go`, `RunAssigner`, `processDeviceResponse`, `buildJSONProfile`, `server/mdm/nanodep/`, `4c207e8`, `server/datastore/mysql/apple_mdm.go`, `server/worker/macos_setup_assistant.go`
- `micromdm/micromdm@904493b`, `dep/`, `platform/dep/sync/depsync.go`, `platform/config/apply_deptoken.go`
- `zentralopensource/zentral@b10dd22`, `zentral/contrib/mdm/dep.py`, `dep_client.py`
