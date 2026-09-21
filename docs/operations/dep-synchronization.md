# Automated Device Enrollment inventory and assignment

The `dep` service maintains Apple server credentials, device inventory and enrollment-profile assignments. The package name remains `dep`; the device-facing enrollment flow is Automated Device Enrollment (ADE). These credentials are separate from [MDM push certificates](certificate-lifecycle.md).

The [workflow diagram](../diagrams/flow-dep-sync-assign.html) shows token setup and recurring reconciliation. The [design decision](../research/decisions/0026-dep-client-sync-and-assignment.md) describes the storage guarantees.

## Token setup and renewal

Apple's [authentication procedure](https://developer.apple.com/documentation/devicemanagement/authenticating-for-automated-device-enrollment) uses a public certificate uploaded to its management portal and an encrypted server token downloaded from it. Retain the matching private key in server storage. The token contains OAuth 1.0a credentials used to obtain an `X-ADM-Auth-Session` session.

The reference server exposes these authenticated administrative routes under `/admin/v1`:

| Route | Behavior |
| --- | --- |
| `PUT /dep/accounts/{name}/keypair` | Stage a token keypair and return its public certificate for the portal. |
| `PUT /dep/accounts/{name}/token` | Import the portal's `.p7m` file; validate it with Apple, then atomically commit credentials, session and any staged-key promotion. |
| `PUT /dep/accounts/{name}/tokens` | Import JSON credentials for development/tests; it has the same ordinary identity rejection rule and no force option. |
| `PUT /dep/accounts/{name}/profile` | Define a profile remotely, then publish it as the local desired target only if the account binding still matches. |
| `GET /dep/accounts/{name}/devices` | Inspect stored inventory with cursor pagination. |
| `POST /dep/accounts/{name}/sync` | Run inventory synchronization, then assignment once. |

A normal renewal preserves inventory, profile definitions, the desired profile and retry state. Import merges into the current locked account so a concurrent local profile/configuration update is retained. If credentials, Apple identity or the decrypting staged key changed during validation, the candidate is rejected; retry after inspecting the winning update. Candidate validation failures do not invalidate unrelated active credentials.

Ordinary `ImportToken` and `StoreTokens` reject a changed established consumer key or Apple identity. Identity uses Apple's [`server_uuid` and `org_id` account metadata](https://developer.apple.com/documentation/devicemanagement/accountdetail), with these project rules:

- Different nonempty old and new server UUIDs or organization IDs mean identity replacement.
- Missing incoming identity metadata preserves the recorded binding; newly supplied metadata can establish a previously unknown value.
- A changed consumer key also means replacement when either the old or new server UUID is unknown. Ordinary imports reject consumer-key changes even when matching known UUIDs demonstrate the same server.

## Explicit server identity replacement

Use `PUT /admin/v1/dep/accounts/{name}/token?force=true` only when intentionally allowing a consumer-key or Apple server identity change. Library callers use `ImportOptions{Force: true}`. Force still validates credentials and rechecks the locked account and staged-key baseline.

A forced import resets state **only when identity actually changes**, and that replacement requires a validated nonempty server UUID. One transaction replaces credentials and account metadata, clears old inventory, profile definitions, desired profile, sessions and assignment retry/lease state, and starts a new full-fetch generation with an advanced cursor revision. Old requests cannot restore the removed state.

The local account name, protocol-version override, creation time and token keypairs survive. If import used the staged key, normal staged-to-current promotion occurs in the same transaction. A forced same-identity renewal preserves inventory and the desired profile. Force is therefore not a general inventory-reset switch.

After a real identity replacement, define the intended profile for the new Apple server and synchronize its devices. The previous desired profile is deliberately cleared, so assignment remains disabled until a new target is configured. Enrollment policies that rely on stored DEP inventory cannot admit the new server's devices until inventory is available.

## Background work and conflicts

`DM_DEP_SYNC_INTERVAL` and `DM_DEP_ASSIGN_INTERVAL` schedule inventory and assignment independently; zero disables the corresponding background operation. The one-shot sync endpoint remains available. `DM_DEP_PROFILE_URL` controls the enrollment URL used when a profile omits one; it defaults to this server's ADE endpoint.

Synchronization begins with [Fetch Devices](https://developer.apple.com/documentation/devicemanagement/fetch-devices), then follows [Sync Devices](https://developer.apple.com/documentation/devicemanagement/sync-devices) cursors. Each accepted page commits with its cursor after account, credential and cursor checks. Only successful completion of the current full-fetch generation reconciles absent devices, including a completed empty snapshot. An interrupted or superseded fetch does not prematurely delete unseen devices.

Assignment snapshots the current account and target, obtains an account lease, and makes bounded Apple requests outside the transaction. Saving outcomes and readback requires the same account identity, OAuth credentials, desired profile and unexpired owned lease. A conflict stops further batches. These checks cannot undo a request Apple already accepted or earlier batches already committed; a later run reconciles current Apple state. Readback also cannot resurrect a device removed by inventory synchronization.

Session caches and session/account-state writebacks use the same credential and identity binding. A response from replaced credentials cannot become the renewed account's current session or authentication state. Remote profile definition likewise rechecks that binding before committing its returned profile and local target.

## Throttling

Inspect per-device outcomes as well as HTTP status. Apple's [Assign Profile documentation](https://developer.apple.com/documentation/devicemanagement/assign-profile) defines per-device `THROTTLED` from protocol version 9 and `retry_after_seconds` from version 10. The assigner persists HTTP 429 deadlines and per-device retries so another worker respects the delay.

Cooldown persistence has a narrower check than business-result persistence: the worker must still own an unexpired lease for the same Apple identity. A same-identity token renewal or desired-profile change does not discard Apple's delay. A stale per-device throttle becomes a conservative account-wide cooldown without saving obsolete outcomes. Identity replacement, lease loss or expiry rejects the old worker's cooldown write.

## SQL upgrades and custom stores

The SQL store applies pending migrations on open unless its caller selects `SkipMigrate`. DEP schema 3 adds stable `dep_account_locks` rows for [SQLite](../../server/depstore/sqlstore/migrations/sqlite/0003_account_locks.sql), [PostgreSQL](../../server/depstore/sqlstore/migrations/postgres/0003_account_locks.sql) and [MySQL](../../server/depstore/sqlstore/migrations/mysql/0003_account_locks.sql). Deployments managing migrations externally must apply the matching migration before running the updated store.

A name lock survives account deletion and reset. Account creation and account/keypair mutations share it, preventing two first imports or keypair staging/promotion from bypassing the transaction's checks. Do not prune these rows as part of account deletion.

No public Go method signature changed, but custom stores must implement the stronger [`Tx.LockAccount`](../../devicemanagement/appleplatformservices/dep/store.go) contract: lock the account name until transaction completion even when the account is absent and `ErrNotFound` is returned, and retain that lock through deletion/recreation. Keypair staging/promotion and account creation must serialize through the same lock. A process-local mutex alone is insufficient for a shared database used by multiple processes.

Implementation evidence: [token validation and reset](../../devicemanagement/appleplatformservices/dep/token.go), [account/credential checks](../../devicemanagement/appleplatformservices/dep/accountfence.go), [assignment lease and cooldowns](../../devicemanagement/appleplatformservices/dep/assignmentstate.go), [SQL name locking](../../server/depstore/sqlstore/syncstate.go), and [reference-server routes and schedules](../../server/internal/app/dep.go).
