# Automated Device Enrollment inventory and assignment

The `dep` service maintains Apple server credentials, device inventory and enrollment-profile assignments. The package name remains `dep`; the device-facing enrollment flow is Automated Device Enrollment (ADE). These credentials are separate from [MDM push certificates](certificate-lifecycle.md).

The [workflow diagram](../diagrams/flow-dep-sync-assign.html) shows token setup and recurring reconciliation. The [design decision](../research/decisions/0026-dep-client-sync-and-assignment.md) describes the storage guarantees.

## Check a caller-supplied ADE profile

The library's `Profile.ValidateForTarget` runs structural validation, then returns
field-specific `Unsupported`, `Unchecked`, `Deprecated` and `Ignored` results.
Structural errors carry `ProfileError.Field`; unsupported target settings are in
the report. Call this before defining or assigning a profile. The Apple client
still performs structural validation only because it has no target facts.

```go
report, err := profile.ValidateForTarget(dep.ProfileTarget{
    Target: support.Target{OS: support.MacOS, Version: osversion.New(26, 0, 0)},
    ProtocolVersion: 2,
})
```

Handle `err`, inspect `report.Unsupported`, and explicitly decide whether the
remaining unchecked dimensions are acceptable. An empty unsupported list alone
is not a complete support claim. The method does not change profile content,
choose profiles, infer missing values, or make Apple requests. Checks apply to
supplied fields, including explicit false booleans; unknown `Extra` fields remain
intact and are unchecked. Wire fields and indexed `skip_setup_items` identify
results; `*` identifies a missing or unsupported target dimension.

Use schema OS names, with `support.IOS` for both iOS and iPadOS. Missing or
unrecognized OS values and missing/invalid versions remain visibly unchecked.
`ProfileTarget` reuses `support.Target` OS/version metadata; its other enrollment
flags are unused because ADE profile values describe the resulting enrollment.
For `is_multi_user=true`, explicitly supply `IsIPad` and `SharedIPadCapable` facts.
The delivered enrollment profile's `com.apple.mdm.per-user-connections` capability
remains unchecked by this API. Unknown protocol versions remain unchecked for
fields requiring protocol version 2.

Skip-key availability, deprecation and removal come from generated
`schema/other` support metadata. Conditional `always-skippable=false` panes retain
an unchecked Setup Assistant context result. This validates the schema's platform
contract; it does not prove that a pane exists on a particular device model.

Apple's [Profile contract](https://developer.apple.com/documentation/devicemanagement/profile)
limits `auto_advance_setup`, `language` and `region` to tvOS and macOS 11 or later,
and the backup/return-to-service flags to iOS/iPadOS and visionOS 26 or later.
It also documents protocol floors, Shared iPad requirements, and the iOS 11/13
supervision, pairing and mandatory-enrollment behavior reported by this API.
Unavailable settings are reported even when Apple would ignore them; ignored
and deprecated iOS settings are reported separately. The validator checks
availability rather than predicting backup behavior: Apple's additional profile
removal behavior from 26.4 is not a different availability boundary.
Apple's [web-view authentication guide](https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views)
specifies iOS/iPadOS 13 and macOS 10.15 introduction boundaries and includes tvOS
and visionOS; unspecified tvOS/visionOS web-view and tvOS auto-advance release
floors remain unchecked. These checks do not validate remote web content,
organization eligibility, language-code registries or hardware enrollment.


## Token setup and renewal

Apple's [authentication procedure](https://developer.apple.com/documentation/devicemanagement/authenticating-for-automated-device-enrollment) uses a public certificate uploaded to its management portal and an encrypted server token downloaded from it. Retain the matching private key in server storage. The token contains OAuth 1.0a credentials used to obtain an `X-ADM-Auth-Session` session.

The reference server exposes these authenticated administrative routes under `/admin/v1`:

| Route | Behavior |
| --- | --- |
| `PUT /dep/accounts/{name}/keypair` | Stage a token keypair and return its public certificate for the portal. |
| `PUT /dep/accounts/{name}/token` | Import the portal's `.p7m` file; validate it with Apple, then atomically commit credentials, session and any staged-key promotion. |
| `PUT /dep/accounts/{name}/tokens` | Import JSON credentials for development/tests; it has the same ordinary identity rejection rule and no force option. |
| `PUT /dep/accounts/{name}/profile` | Define and retain a caller profile without changing the default. `?default=true` also selects it after account-binding and desired-revision checks. |
| `GET /dep/accounts/{name}/profiles` | Inspect the account's profile definitions with cursor pagination. |
| `GET /dep/accounts/{name}/targets` | Inspect the default, explicit serial choices and revision. |
| `PUT /dep/accounts/{name}/default-profile` | Select an existing account profile with `{"profile_uuid":"UUID"}`. |
| `DELETE /dep/accounts/{name}/default-profile` | Remove the local default; explicit serial choices remain. |
| `PUT /dep/accounts/{name}/targets/{serial}` | Set an explicit physical-serial choice with `{"profile_uuid":"UUID"}`. |
| `DELETE /dep/accounts/{name}/targets/{serial}` | Remove the override and resume default resolution; makes no remote clear/disown call. |
| `GET /dep/accounts/{name}/devices` | Inspect stored inventory with cursor pagination. |
| `POST /dep/accounts/{name}/sync` | Run inventory synchronization, then assignment once. |

A normal renewal preserves inventory, profile definitions, the default, all explicit serial choices and retry state. Import merges into the current locked account so a concurrent local profile/configuration update is retained. If credentials, Apple identity or the decrypting staged key changed during validation, the candidate is rejected; retry after inspecting the winning update. Candidate validation failures do not invalidate unrelated active credentials.

Ordinary `ImportToken` and `StoreTokens` reject a changed established consumer key or Apple identity. Identity uses Apple's [`server_uuid` and `org_id` account metadata](https://developer.apple.com/documentation/devicemanagement/accountdetail), with these project rules:

- Different nonempty old and new server UUIDs or organization IDs mean identity replacement.
- Missing incoming identity metadata preserves the recorded binding; newly supplied metadata can establish a previously unknown value.
- A changed consumer key also means replacement when either the old or new server UUID is unknown. Ordinary imports reject consumer-key changes even when matching known UUIDs demonstrate the same server.

## Explicit server identity replacement

Use `PUT /admin/v1/dep/accounts/{name}/token?force=true` only when intentionally allowing a consumer-key or Apple server identity change. Library callers use `ImportOptions{Force: true}`. Force still validates credentials and rechecks the locked account and staged-key baseline.

A forced import resets state **only when identity actually changes**, and that replacement requires a validated nonempty server UUID. One transaction replaces credentials and account metadata, clears old inventory, profile definitions, desired default and serial choices, sessions and assignment retry/lease state, and starts a new full-fetch generation with an advanced cursor revision. Old requests cannot restore the removed state.

The local account name, protocol-version override, creation time and token keypairs survive. If import used the staged key, normal staged-to-current promotion occurs in the same transaction. A forced same-identity renewal preserves inventory and all desired choices. Force is therefore not a general inventory-reset switch.

After a real identity replacement, define the intended profile for the new Apple server and synchronize its devices. The previous desired profile is deliberately cleared, so assignment remains disabled until a new target is configured. Enrollment policies that rely on stored DEP inventory cannot admit the new server's devices until inventory is available.

## Persist explicit profile choices

Definitions coexist within an account. Define caller profiles first; selecting a
default is separate and optional. Change desired state within `Store.Update` after
`Tx.LockAccount`, read the current account, edit `ProfileTargets[serial]` or the
`ProfileUUID` default and call `PutAccount`. A target UUID must reference a stored
definition in that account. Stores own `ProfileRevision`; do not supply revisions
as a compare-and-swap mechanism. The shared contracts define revision advancement.

```go
err := store.Update(ctx, func(tx dep.Tx) error {
    if err := tx.LockAccount(ctx, "school"); err != nil { return err }
    account, err := tx.GetAccount(ctx, "school")
    if err != nil { return err }
    if account.ProfileTargets == nil { account.ProfileTargets = map[string]string{} }
    account.ProfileTargets["SERIAL-A"] = selectedUUID
    return tx.PutAccount(ctx, account)
})
```

Handle `err`. Call `Profile.ValidateForTarget` with explicit facts before defining
or selecting a profile when those facts are available. Device-family and OS fields
never choose a profile automatically. Both assignment and reference admission use
`Account.DesiredProfile`: serial override, configured account default, then no
action. An override works without a default. Deleting an override falls through
to the default; if no default exists the device retains Apple's observed profile
and is ineligible for profile-based admission. Removing desired state never calls
Apple's clear-profile or disown endpoints. Selected definitions cannot be deleted.
The production ADE profile endpoint returns HTTP 403 for an enrollment-policy
denial; unavailable DEP state returns HTTP 500 and does not issue a profile.

## Background work and conflicts

`DM_DEP_SYNC_INTERVAL` and `DM_DEP_ASSIGN_INTERVAL` schedule inventory and assignment independently; zero disables the corresponding background operation. The one-shot sync endpoint remains available. `DM_DEP_PROFILE_URL` controls the enrollment URL used when a profile omits one; it defaults to this server's ADE endpoint.

Synchronization begins with [Fetch Devices](https://developer.apple.com/documentation/devicemanagement/fetch-devices), then follows [Sync Devices](https://developer.apple.com/documentation/devicemanagement/sync-devices) cursors. Each accepted page commits with its cursor after account, credential and cursor checks. Only successful completion of the current full-fetch generation reconciles absent devices, including a completed empty snapshot. An interrupted or superseded fetch does not prematurely delete unseen devices.

Assignment snapshots the current account and target, obtains an account lease, and makes bounded Apple requests outside the transaction. Saving outcomes and readback requires the same account identity, OAuth credentials, desired-profile revision and unexpired owned lease. A conflict stops further batches. These checks cannot undo a request Apple already accepted or earlier batches already committed; a later run reconciles current Apple state. Readback also cannot resurrect a device removed by inventory synchronization.
Batches contain only serials resolving to the same UUID. The revision advances on
serial/default changes, including changes away and back, and writes to selected
definitions. A known per-device Apple throttle survives target changes; ordinary
failure backoff can reset when selecting another UUID.

Before a mutation the assigner persists `AssignmentPending`. After a lost response,
failed outcome commit, stale target response, omitted per-serial outcome or
unrecognized status, another run reads device details
before resubmission. Unavailable or inaccessible readback blocks retries. A
confirmed current-target success is recorded and published without a duplicate
assignment. `AssignResult.Pending` counts missing/unrecognized outcomes separately
from terminal failures. The marker records uncertainty, not a successful Apple outcome.

Session caches and session/account-state writebacks use the same credential and identity binding. A response from replaced credentials cannot become the renewed account's current session or authentication state. Remote profile definition likewise rechecks that binding before committing its returned profile and local target.

## Throttling

Inspect per-device outcomes as well as HTTP status. Apple's [Assign Profile documentation](https://developer.apple.com/documentation/devicemanagement/assign-profile) defines per-device `THROTTLED` from protocol version 9 and `retry_after_seconds` from version 10. The assigner persists HTTP 429 deadlines and per-device retries so another worker respects the delay.

Cooldown persistence has a narrower check than business-result persistence: the worker must still own an unexpired lease for the same Apple identity. A same-identity token renewal or desired-profile change does not discard Apple's delay. A stale per-device throttle becomes a conservative account-wide cooldown without saving obsolete outcomes. Identity replacement, lease loss or expiry rejects the old worker's cooldown write.

## SQL upgrades and custom stores

The SQL store applies pending migrations on open unless its caller selects `SkipMigrate`. DEP schema 3 adds stable `dep_account_locks` rows for [SQLite](../../server/depstore/sqlstore/migrations/sqlite/0003_account_locks.sql), [PostgreSQL](../../server/depstore/sqlstore/migrations/postgres/0003_account_locks.sql) and [MySQL](../../server/depstore/sqlstore/migrations/mysql/0003_account_locks.sql). Deployments managing migrations externally must apply the matching migration before running the updated store.

DEP schema 4 adds `dep_profile_choices` for serial choices and their revision in
[SQLite](../../server/depstore/sqlstore/migrations/sqlite/0004_profile_targets.sql),
[PostgreSQL](../../server/depstore/sqlstore/migrations/postgres/0004_profile_targets.sql)
and [MySQL](../../server/depstore/sqlstore/migrations/mysql/0004_profile_targets.sql).
Account defaults stay in their existing column. Installation is a single replayable
`CREATE TABLE IF NOT EXISTS`: if MySQL commits the table before migration bookkeeping
fails, rerun migration. Existing choices and revisions survive replay. No enrollment,
credential, keypair, inventory, queue, schedule or audit reset is required.

A name lock survives account deletion and reset. Account creation and account/keypair mutations share it, preventing two first imports or keypair staging/promotion from bypassing the transaction's checks. Do not prune these rows as part of account deletion.

Custom stores must retain `ProfileTargets`, validate serial/UUID entries and
account-scoped definition references, maintain `ProfileRevision` with
`ProfileTargetRevision`, advance it on selected-definition writes, and reject
selected-definition deletion. They must also implement the stronger [`Tx.LockAccount`](../../devicemanagement/appleplatformservices/dep/store.go) contract: lock the account name until transaction completion even when the account is absent and `ErrNotFound` is returned, and retain that lock through deletion/recreation. Keypair staging/promotion and account creation must serialize through the same lock. A process-local mutex alone is insufficient for a shared database used by multiple processes.

Implementation evidence: [token validation and reset](../../devicemanagement/appleplatformservices/dep/token.go), [account/credential checks](../../devicemanagement/appleplatformservices/dep/accountfence.go), [assignment lease and cooldowns](../../devicemanagement/appleplatformservices/dep/assignmentstate.go), [SQL name locking](../../server/depstore/sqlstore/syncstate.go), and [reference-server routes and schedules](../../server/internal/app/dep.go).
