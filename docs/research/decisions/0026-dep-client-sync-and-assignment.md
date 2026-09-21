# 0026: DEP client, device sync, and profile assignment

## Context

The device enrollment service uses account-scoped OAuth 1.0a credentials, rotating sessions and cursors. Device sync and profile assignment must recover from partial failures.

## Decision

`dep.Client` resolves accounts through a store, signs requests, coordinates session refresh per account and retries authentication once. Sessions, response adoption and account-state writes are bound to the exact OAuth credentials and Apple account identity that initiated the request. A renewal cannot install an old cached session or an obsolete authentication failure.

Token validation runs outside the write transaction. The commit locks the account name, rechecks the credential and identity baseline and any staged keypair, then merges into the current account. Concurrent profile/configuration changes survive a renewal. An invalid candidate does not mark unrelated active credentials invalid.

`StoreTokens` and ordinary `ImportToken` reject an established consumer-key or Apple identity change. `ImportOptions.Force` explicitly permits replacement. An actual identity change requires a validated nonempty `server_uuid` and atomically clears the old server's devices, profile definitions, desired target, sessions, cursor and assignment state. A new fetch generation and incremented cursor revision reject old workers. The local account name, protocol version, creation time and token keypairs survive. A same-identity renewal, including a forced renewal, preserves inventory and the desired profile. Omitted `server_uuid` or `org_id` values do not erase an established binding. The [operations guide](../../operations/dep-synchronization.md) specifies the identity comparison and reset procedure.

`Syncer` performs fetch then incremental sync and commits cursors with each page. Each full fetch persists a generation and marks returned serials. Only successful completion of the current generation tombstones active devices absent from that generation, including an empty snapshot. Locked account/credential checks and cursor revisions reject superseded responses. An interrupted fetch resumes without deleting unseen inventory early.

`Assigner` compares stored profile state with desired state and records per-device outcomes. Every business-result commit rechecks the account identity, OAuth credentials, desired profile and renewable assignment lease. A conflict stops further batches; earlier committed batches and remote requests already accepted by Apple cannot be undone. Assignment readback cannot restore a device removed by sync. The reference server also checks the account binding after defining a profile remotely, before publishing that profile as the target.

Account retry deadlines and failure counts live in the store, so replacement workers respect earlier throttling. Cooldown writes require the same Apple identity and an unexpired owned lease, but tolerate same-identity credential renewal or a changed desired profile. A stale per-device `THROTTLED` response becomes a conservative account cooldown without saving its obsolete assignment outcomes. Identity reset or lease loss prevents those writes. Bounded network requests run outside transactions.

The reference server schedules sync and assignment independently; zero disables the corresponding background operation. API profiles retain unknown fields and validate documented combinations and setup keys.

The SQL schema persists cursor revision/generation fields, device fetch markers and assignment state. Schema 3 adds `dep_account_locks` for SQLite, PostgreSQL and MySQL. Its stable per-name row serializes account creation, account/keypair mutations and reset even while the account is absent; account deletion does not delete that lock row. This avoids relying on a lock on a nonexistent account row.

Custom stores must implement `MarkFetched`, `AssignmentState`, `PutAssignmentState` and transactional `LockAccount` with the shared contract semantics. `LockAccount` returns `ErrNotFound` for an absent account but **still holds the name lock until transaction completion**, including delete/recreate. Account creation and keypair staging/promotion must participate in that same lock. No Go interface signature changed; this is a stronger storage contract. The reference server's SQL store migrates to schema 3 on open; external migration management must apply the matching dialect's migration before using these semantics.

Apple's [Assign Profile response](https://developer.apple.com/documentation/devicemanagement/assign-profile)
can return HTTP 200 with per-device `THROTTLED` results and
`retry_after_seconds` in protocol version 10. Check device outcomes in addition to
HTTP status. The [assigner](../../../devicemanagement/appleplatformservices/dep/assigner.go)
and [persisted assignment state](../../../devicemanagement/appleplatformservices/dep/assignmentstate.go)
coordinate retry eligibility; the [syncer](../../../devicemanagement/appleplatformservices/dep/syncer.go)
owns inventory generations and stale-response rejection.

## Rationale

Committing a cursor with its data supports at-least-once page delivery. Assignment based on current state handles re-fetches and server moves as well as newly added devices. Typed errors distinguish operator action from transient retry.

## Constraints

API types are maintained from Apple's documentation because this service's JSON schemas are not in the pinned YAML submodule. `dep` is retained as an API/package identifier; user-facing enrollment terminology is Automated Device Enrollment. Live Apple service behavior requires integration validation.

## Verification

Client, PKI, syncer and assigner tests use the independent fake service for signature verification, session rotation, pagination, injected errors and per-serial outcomes. Store contracts cover resumable and empty snapshots, persisted backoff, concurrent account locking and rollback across memory, SQLite, PostgreSQL and MySQL. Worker race tests cover stale sync responses, expired claims, readback after removal, desired-profile changes, concurrent renewal/import, staged-key replacement and explicit identity reset. Shared memory/SQL account-fence contracts exercise stale success, authentication and throttle responses; SQL contracts cover absent-name creation locks and keypair staging. End-to-end tests cover assignment and ADE enrollment.

## References

- [appleplatformservices/dep](../../../devicemanagement/appleplatformservices/dep)
- [storage/dep](../../../devicemanagement/storage/dep)
- [server/depstore](../../../server/depstore)
- [Account/credential fences](../../../devicemanagement/appleplatformservices/dep/accountfence.go)
- [Atomic token commit and reset](../../../devicemanagement/appleplatformservices/dep/token.go)
- [SQL account-name locks](../../../server/depstore/sqlstore/syncstate.go)
- [DEP operations](../../operations/dep-synchronization.md)
- <https://developer.apple.com/documentation/devicemanagement/device-assignment>
- <https://developer.apple.com/documentation/devicemanagement/authenticating-for-automated-device-enrollment>
- <https://developer.apple.com/documentation/devicemanagement/accountdetail>
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
