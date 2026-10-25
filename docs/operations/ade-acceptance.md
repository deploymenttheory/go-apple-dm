# Automated Device Enrollment acceptance

The maintained `dmctl lab` modules validate the MDM server's integration with
Apple's device-assignment service and a designated ADE-enrolled device. They reuse
`dep.Client`, the reference synchronization/assignment worker, ordinary authorized
administration routes and the native command/DDM evidence path. They do not manage
an ABM organization or require consumers to adopt a universal inventory model.

| Module | Operation | Evidence boundary |
|---|---|---|
| LIVE-010 | Separate server-token authentication, account binding, paginated full/delta enumeration, designated device and current profile readback | Read-only Apple API evidence. No profile definition, assignment or reference one-shot sync. |
| LIVE-011 | Explicit profile definition, independent readback, designated serial selection, reference synchronization/assignment and restoration | API assignment evidence and separate cleanup result. Requires explicit mutation opt-in. |
| LIVE-012 | Stored ADE issuance, native identity/check-ins, effective profile, fresh APNs-triggered inventory and applicable DDM | One real device/platform/OS/build, with independent DDM exchange and cleanup results. |

API success cannot establish Setup Assistant activation or hardware compatibility.
Local OAuth, APNs and device fixtures exercise the same server composition but
retain `simulated` mode in private evidence. Missing prerequisites and empty
populated-device checks remain blocked/unexercised. Authorization errors fail.
No module disowns devices or changes Activation Lock.

## Prepare the private workspace

Use a running [live reference workspace](../../test-lab/README.md#live-workspace-and-existing-material)
and its normal administrator credential and HTTPS trust. The separate ADE
[server token](dep-synchronization.md#token-setup-and-renewal) must already be
imported into a named reference account. An ABM OAuth key, Apps and Books sToken
or APNs certificate cannot substitute for it.

Place the **decrypted OAuth server-token JSON** in `ade/tokens.json`; the encrypted
portal `.p7m` is imported/decrypted through the existing token workflow. Keep the
matching private key and portal token in their original private storage. This
acceptance client holds its own session in memory and independently checks the
Apple `server_uuid` against the selected reference account before work.

```sh
PRIVATE_LAB=/absolute/path/to/private/live-workspace
install -d -m 700 "$PRIVATE_LAB/ade"
install -m 600 /absolute/path/to/decrypted-ade-token.json "$PRIVATE_LAB/ade/tokens.json"
```

Create `ade/acceptance.json` with mode 0600, replacing each example value with the
designated lab resource. The serial is the sole physical correlation key; the
opaque `server_uuid` binds the account and `device_id` addresses its native MDM
protocol channel. Those identifiers serve different purposes.

```json
{
  "account": "ade-lab",
  "server_uuid": "APPLE-SERVER-UUID",
  "serial": "DESIGNATED-SERIAL",
  "platform": "macOS",
  "os_version": "27.0",
  "device_id": "NATIVE-DEVICE-ENROLLMENT-ID",
  "identity": "scep",
  "allow_profile_assignment": false,
  "exclusive_account_quiescent": false,
  "exercise_ddm": false
}
```

Valid platform labels are `macOS`, `iOS`, `iPadOS`, `visionOS` and `tvOS`.
`identity` is `scep` or `acme`, matching the identity actually issued by ADE.
`device_id` and identity are required for LIVE-012. Platform and explicit OS
version are required for LIVE-011 profile validation. Never substitute an
operator-entered value for missing native evidence.

The account must contain the designated serial. Grant `listDEPAccounts` and `readDEP`
for read-only acceptance. LIVE-011 additionally needs `manageDEPProfiles` and
`syncDEPDevices`. LIVE-012 uses the existing scoped `readEnrollment`,
`enqueueCommand.DeviceInformation` and `pushEnrollment` permissions, plus applicable
DDM declaration/set/assignment, notification and status-read permissions from
the [admin action catalogue](../../server/internal/app/admincatalog.go).
It grants no permissions implicitly.

Run the read-only check independently:

```sh
make lab-build
DMCTL="$PWD/test-lab/local/bin/dmctl"
"$DMCTL" lab run -workspace "$PRIVATE_LAB" -modules LIVE-010
```

Preserve the run directory,
source revision, binary hash and private full/delta pages. A delta with no changes
or Apple's legitimate exhausted cursor does not prove that changed-record
processing occurred; `delta_changes_exercised` records that distinction.

## Opt in to designated assignment and restoration

Use a dedicated account with **no default profile and no serial targets except
possibly the designated serial**. Set both `DM_DEP_SYNC_INTERVAL` and
`DM_DEP_ASSIGN_INTERVAL` to `0` in the private workspace's `Settings` and restart
before testing. Stop other workers/operators for the duration. Inspect the account
and targets using the ordinary administration API before attesting quiescence.
A one-shot reference sync can assign every eligible desired target in its account.

The API has no compare-and-swap for target selection. Account identity, target
revision and ownership are revalidated before each mutation and cleanup, but a
read followed by a write cannot exclude another writer. The explicit
`exclusive_account_quiescent: true` attestation is an operator prerequisite.
An observed concurrent change blocks restoration rather than overwriting it.

Create `ade/profile.json` (0600) with the caller's benign, structurally valid ADE
profile definition, for example:

```json
{
  "profile_name": "Designated ADE acceptance",
  "url": "https://mdm.example.test/enroll/ade",
  "org_magic": "dedicated-lab"
}
```

Use the device-reachable HTTPS enrollment URL for this server. The module checks
`Profile.ValidateForTarget` with the configured platform/OS version and refuses
unsupported or unchecked fields. It rejects an existing `profile_uuid`, a
nonempty `devices` array that could implicitly assign serials during definition,
and return-to-service. It defines without selecting an account default.

Set `allow_profile_assignment` and `exclusive_account_quiescent` to `true`, then:

```sh
"$DMCTL" lab run -workspace "$PRIVATE_LAB" -modules LIVE-011
```

Each invocation creates its own private `assignment-*` directory and an exclusive
workspace `ade/pending-assignment.json` fence before mutation. The immutable fence
contains the original account/serial/remote assignment/local choice and an absolute
private evidence-directory locator. Known returned profile UUIDs and subsequent
observations are separate files there; the initial fence does not claim later
progress it never observed.

Successful cleanup restores the original remote profile or explicit unassigned
state, restores presence/absence of the original local serial choice, then
reconciles the persisted reference record. Missing remote profile-status fields
are unknown, not unassigned. The created definition remains retained because
this check has no remote profile-deletion operation.

A lost response, pending per-device outcome, changed ownership or failed cleanup
retains the fence and unresolved cleanup evidence. A currently matching remote
UUID cannot establish that an unanswered request will never arrive later. The
module skips automatic inverse replay after unknown outcomes; reruns, even under
another report parent, cannot overwrite the original intent or start another
mutation. Stop the original process and all workers, inspect the located evidence
and independent Apple/reference state, and reconcile manually before removing
that exact fence. Removing the marker alone does not settle the original request.
Do not run LIVE-012 while an assignment test is temporarily changing its profile.

## Validate real enrollment on each platform

Use an operator-approved spare device already assigned to this Apple management
server. Erasure and Setup Assistant interaction are manual prerequisites; these
modules never erase a device or trigger enrollment. Follow Apple's
[ADE preparation procedure](https://support.apple.com/guide/deployment/dep73069dd57/web)
for the selected model and release. Use the existing signed MachineInfo ADE
endpoint and configured admission/identity workflow; manually installing a profile
cannot manufacture stored ADE provenance.

| Platform | Execute before LIVE-012 | Retain |
|---|---|---|
| macOS | Complete Remote Management in Setup Assistant on the assigned Mac; use its actual ADE-issued SCEP/ACME identity. | Mac model, OS/build, Setup Assistant observation and resulting native channel. |
| iOS | Complete Remote Management on the assigned iPhone or supported iPod touch. | Exact model and release; device-channel check-ins. |
| iPadOS | Complete Remote Management on the assigned iPad; keep its result separate from iOS. | Exact iPad model/release and configured enrollment choices. Shared iPad scenarios require their own acceptance. |
| visionOS | Complete organization enrollment during setup on an eligible assigned Apple Vision Pro/release. | Exact hardware/release and the observed enrollment path. |
| tvOS | Complete organization enrollment on the assigned Apple TV using its applicable setup workflow. | Exact Apple TV model/release and resulting native channel. |

For each device, set the scope's serial, platform, OS version, `device_id` and
actual identity method. The effective local desired profile must match Apple's
independently observed profile UUID. Set `exercise_ddm: true` for applicable DDM,
then execute one run per device:

```sh
"$DMCTL" lab run -workspace "$PRIVATE_LAB" \
  -modules LIVE-012 -device-id NATIVE-DEVICE-ENROLLMENT-ID
```

LIVE-012 reads stored `EnrollmentMethod`, serial, protocol bindings, pinned
certificate/expiry, Authenticate and TokenUpdate evidence under the existing
`readEnrollment` grant. Missing/legacy issuance remains unknown. It then requires
a fresh APNs-accepted `DeviceInformation` command acknowledgment containing the
matching serial, native product family, nonempty OS/build and typed supervision
observation. True, false and missing supervision remain distinct; ADE membership
never supplies a substitute. Identity continuity is checked again after native
MDM and DDM exchanges. User Enrollment and manually delivered profiles cannot
satisfy this ADE issuance check.

Applicable DDM is decided per generated feature/platform/version/channel support
for management status subscriptions, simple activation and OS/build status items.
Supported tvOS versions can exercise these features; managed-app DDM limitations
are a separate feature. Unknown required capabilities block proof; a genuinely
unsupported feature is recorded as unsupported while retaining native MDM proof.
A unique subscription/activation requires fresh valid active declaration status
and values matching the fresh native OS/build. Cleanup removes only its temporary
assignment/declarations and waits for native removal status.

`hardware-*/platform-results.json` contains five separate platform rows. The
selected target can pass or fail only its own row; all others remain unexercised.
Private issuance, effective-profile, command/push/raw acknowledgment and DDM
status files support that result. `ddm-phases.json` and `ddm_cleanup` preserve
native exchange success independently of unresolved administrative cleanup.
Inspect `results.json`, `junit.xml` and `report.html` together with this private
phase evidence. Never copy raw serials, tokens or identity material into public
issue notes.

## Outstanding live validation

[Ticket #318](https://github.com/deploymenttheory/go-apple-dm/issues/318) remains
open until separate ADE credentials, an operator-designated quiescent account and
approved platform devices are supplied and the corresponding live evidence is
reviewed. Automated fixture passes prove local integration/error contracts only.
Disowning, Activation Lock, software-update gates, enrollment web authentication,
Shared iPad and app/book acquisition each require their own explicitly scoped
acceptance; they are not implied by these three modules.
