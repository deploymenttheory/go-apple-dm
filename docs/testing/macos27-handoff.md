# macOS 27 validation handoff

Current host: **macOS 27.0 (26A428)**, Apple silicon, Go 1.27.1.
The implementation in [PR #64](https://github.com/deploymenttheory/go-apple-dm/pull/64)
is merged. Native validation follow-up continues on
`feat/macos27-compatibility-osversion`.
The original preparation was completed on macOS 26.6.2 on 16 September 2026;
its evidence is retained in [preparation validation](macos27-prep-validation.md).

## Readiness work — 18 September 2026

The user clarified that the acceptance target is the **library and reference
server**, not certification of Apple's MDM/DDM feature implementations. The
previous native behavior campaign went beyond that target and is stopped.
Preserve its evidence and limitations without treating every incomplete Apple
feature check as a project blocker.

### Project acceptance criteria

1. **Library contract:** generated models and exact wire names/types match the
   pinned sources; encoding/decoding, required and conditional validation, and
   OS/version/channel/enrollment availability have named regression coverage.
2. **Reference-server contract:** API validation, inventory-derived eligibility,
   MDM enqueue/dispatch/results, DDM manifests/tokens/activations, asset delivery,
   status/error ingestion and persistence behave correctly. Include authentication,
   failure paths, cleanup and restart durability where the server owns them.
3. **Native interoperability:** representative physical macOS 27 command and DDM
   exchanges confirm the server emits usable payloads and handles genuine replies,
   declaration status and reports. Preserve the existing enrollment, command,
   apply/remove, profile-asset and cache-report results. A valid/active declaration
   alone does not demonstrate every field or a deferred asset fetch; combine it
   with wire-level regressions and state the exact integration coverage.
4. **Change validation:** close demonstrated repository defects, verify generated
   output, run affected checks and identify the revision for every result. Do not
   claim the earlier full CI matrix applies to later untested changes.

Siri answers, Calendar editing, privacy-dialog variants, VPN/filter enforcement,
actual OS installation and broad Apple feature behavior are outside this sign-off.
A failing native exchange still requires investigation when our payload, delivery
or response handling may be responsible. Missing providers, tokens or VM APNs
alone do not block the project: disclose the affected native integration gap and
verify the repository-owned paths with protocol tests. Do not build an SSO
provider or restore a VM merely to finish the superseded behavior checklist.

Current next steps are to audit these four criteria against the existing evidence,
address actual library/server gaps, and produce a readiness conclusion with
explicit limitations. Full project sign-off has not yet been asserted.

### Approved test placement

The user confirmed that no spare physical Mac is available and authorized
non-destructive validation on the working Mac, excluding wipes. Their numbered
disposition of the outstanding test list is:

- **18 ManagedApp:** blocked by the current enrollment type/rights; preserve it.
- **20 SSO web authentication and 21 SSO login/security:** blocked. A Platform
  SSO test implementation and provider/identity setup must be prepared first.
- **25 native management status and 26 deprecated profile compatibility:**
  blocked on the physical Mac; use the macOS 26 VM. Respect each field's actual
  availability: a 26 result cannot establish a 27-only status or native behavior.
- **28 enhanced diagnostics:** blocked because there is no AppleCare token.
- **1–17, 19, 22–24 and 27:** considered viable for physical testing, subject to
  the stated non-destructive scope and actual prerequisites. Use disposable
  fixtures, preserve the baseline and verify cleanup. The previous blanket
  restriction of these cases to a guest is superseded by this authorization.

These permissions record where tests may run, not a requirement to run all 28
cases. The later project-scope clarification takes precedence. No further physical
UI/feature test is currently scheduled. Preserve the host enrollment, FileVault
and login policy; no wipe is authorized. Inspection at the scope correction found
zero physical-device test assignments and the current fixture marked removed.

The binary rule that disrupted unrelated executables remains a recorded native
failure. Its saved payload already contained a specific CDHash and signing ID;
the separate library identifier-validation defect was corrected and regression
tested. Do not infer that the correction explains the native failure.

The latest complete CI matrix passed at `eb5208a`, including thirteen named OS 27
contracts and the unchanged 95% gate. The subsequent binary validation correction
passed affected race tests, all thirteen contracts, generator verification and
lint locally. The 50-case source inventory and 32 fixtures establish named source
and protocol coverage, not 32 native behavior passes. The live report identifies
which physical results demonstrate delivery, asset use, status ingestion or
supplementary Apple feature behavior.

## Current continuation

The guest and feature-test procedures below are retained history. The project
acceptance criteria above supersede their instructions to continue native behavior
or VM recovery work.

Read the [live validation report](macos27-live-validation.md) and
[Apple feature coverage](../operations/apple-os27-coverage.md). Preserve conclusive
results and repeat only unresolved variants or cases affected by code changes.
The user authorized a disposable macOS 27 VM built with Guestweave, with its
window visible during testing, and suitable hardware-dependent checks on the host.
Use the guest for binary controls and destructive enrollment/update cases.
The guest disk is limited to **40 GB**, as requested. Guestweave changes needed
for this lab belong in that project's PRs; the telemetry startup correction is
in merged [Guestweave PR #181](https://github.com/deploymenttheory/guestweave-cli-macos/pull/181).
Native guest provisioning was subsequently added in merged
[Guestweave PR #182](https://github.com/deploymenttheory/guestweave-cli-macos/pull/182)
and released in **v1.1.0**, using `go-bindings-macosplatform` **v0.20.0**.
The fresh `macos27-provisioned` guest was restored and first-boot provisioned from
that release in a visible native window. Account creation, automatic login and
SSH pass. Its new SCEP profile is user-approved, but APNs BAA key generation still
fails before enrollment and after reboot. Authenticate reached the server at
12:46:40 UTC; TokenUpdate remains absent and the enrollment is disabled, with no
user channel or assigned declarations. See the
[controlled retry](macos27-live-validation.md#native-provisioning-retry--guestweave-v110).
The Guestweave provisioning omission is fixed; APNs recovery is **not** established.

An isolated **Tart 2.37.0** copy of that same guest also fails APNs identity
generation after cold boots with both suspendable and ordinary device
configurations. The vendor signature was retained and verified. See the
[controlled comparison](macos27-live-validation.md#controlled-tart-comparison).
The disposable Tart comparison disk was reclaimed after diagnostic capture; its
configuration, NVRAM and logs are retained. The original provisioned guest is
stopped and the original acceptance guest remains suspended. The separate macOS **26.6.2 / 25G83** fallback
now passes native device/user TokenUpdate, three timed APNs inventory commands
(including a cold boot), and **LIVE-003 / LIVE-004**, with zero declarations left
assigned. See the [fallback baseline](macos27-live-validation.md#macos-26-fallback-baseline).
The authorized upgrade of that same guest to **27.0 / 26A428** completed, retaining
its device/user identities and user-approved enrollment. APNs still fails: two
fresh inventory commands timed out with verified HTTPS, including after a full
cold boot and login. Native logs report failure to read the existing APNs reference
key (`-25308`). See the [upgrade result](macos27-live-validation.md#same-identity-upgrade-to-macos-27).
The fallback uses an **80 GB sparse disk**;
both existing guests keep their **40 GB** disks and snapshots. Private continuation
helpers and evidence are in `guestweave/apns-recovery-20260917/` under the lab root.
An offline `macos26-apns-working` checkpoint was taken after all baseline checks
and DDM cleanup. The completed 27.0 installer
was reclaimed after shutdown, initially leaving about 22.4 GiB free on the host. Preserve
the working checkpoint and identity. The first two post-upgrade timeouts had an
expired SSH tunnel and are inconclusive; the later two failures have verified
HTTPS before and after each test. The private helper now uses persistent
forwarding with keepalives. Do not count retained enabled channels or the old 26
TokenUpdate as current APNs readiness. The process-pause storage guard is retired;
never suspend the Virtualization helper process with SIGSTOP.

The [open/closed UTM/Tart review](macos27-vm-incidents.md) is complete. A direct
Terminal launch as the logged-in host user also failed the fresh post-login push
deadline (45.77 seconds); the guest has Recovery, so stripped-image reports do
not match it. Queue inspection found five late acknowledgements across the older
and pre-login tests, all reporting 27.0 inventory. Their deadlines still failed;
only the remaining pending test request was cleared, preserving completed results.

The authorized **27.2 beta / 26B5086k** test is complete. Restoring the working 26
checkpoint recovered **47.78 GiB** host free space and a fresh 26 push passed.
Apple's beta package signature, member checksums and embedded target were verified
before the native upgrade. The upgraded beta retains the VM/user identities and
user-approved MDM enrollment, but **APNs readiness still fails** on this 27.0 host.
The first request timed out at 45.58 seconds; after a cold boot/login, one request
acknowledged at 20.32 seconds, followed by independent failures at 45.72 and
45.77 seconds. TLS was verified before and after every attempt. Native logs still
show reference-key `-25308`, BAA failures and no APNs connection after cold boot.
See the [beta result](macos27-live-validation.md#macos-272-beta-result).

The beta guest is **shut down**, with its 80 GB disk preserved. Completed media
was reclaimed before the cold-boot test; final host free space was **21.97 GiB**.
Only two pending expired beta requests were cleared; completed and late results
remain intact. There are zero declarations. The working 26 checkpoint and both
original 40 GB guests remain preserved. Private `beta-outcome-summary.json` and
`continuation-state.json` record the final state. Do not rerun the removed
installer's download journal or the retired process-pause guard. A 27.2 host and
fresh beta IPSW restore remain untested; host OS changes are outside this
guest-only authorization. No 27 LIVE-003 / LIVE-004 acceptance is claimed.

The original comparison guest and evidence remain preserved. The
`macos27-acceptance` guest is now suspended; its recorded OS is 27.0 / 26A428. Its disk
is exactly 40,000,000,000 bytes, with four virtual CPUs and 4 GiB RAM. Setup is
complete and the user installed its fresh SCEP profile with reviewed rights 4115.
Guest Settings and `profiles` confirm user-approved enrollment, but command/DDM
testing is **blocked**: Authenticate reached the server, while APNs TokenUpdate
did not. The guest's `apsd` cannot generate its BAA identity key
(`errSecInteractionNotAllowed`, -25308). The server correctly retains the guest
as disabled, with no installing-user channel or assigned declarations. Do not
manually enable it, invent push credentials or count profile installation as
successful command delivery. This matches the reports in
[Apple's developer forum](https://developer.apple.com/forums/thread/840500);
the external thread is corroboration, not an Apple-confirmed fix or diagnosis.

Private configuration, credentials, snapshots and evidence are under
`test-lab/local/apple27/guestweave/`. The snapshots are `clean-install` (before
enrollment) and `enrollment-apns-blocked` (live state). The guest SSH connection
forwards its loopback ports 8443 and 9443 to the host's existing lab listeners,
preserving TLS verification. Only the guest's exact UUID/serial was added to
admission; the physical host's rule and enrollment remain intact. The canonical
lab now runs binaries built from `eb5208a`, with source/binary manifests saved.
No new native feature pass is recorded for this guest.

The v1.1.0 retry's private evidence, provisioning credentials and isolated SSH/UI
helpers are in `test-lab/local/apple27/guestweave/provisioned-lab/`. Its source
checkout is `guestweave/provisioning` at release revision `459299f`; the private
`/private/tmp/dm-guestweave-provisioned.sh` wrapper selects that binary and the same
short storage alias. Admission now retains the exact rules for the physical host,
both original guests and the fallback. Preserve all four rules and the canonical
server data.
The fresh guest also remains within the 40 GB disk limit; no declaration should
be assigned until actual TokenUpdate and tracked inventory establish readiness.
Its `restored-before-first-boot` snapshot preserves the unconfigured restore;
`provisioned-enrollment-apns-blocked` is an offline checkpoint after the retry
and orderly guest shutdown. The live snapshot's free-space guard refused a RAM
capture, so no live-memory snapshot is claimed. The guest was reopened in its
native window after the offline checkpoint. Subsequent boots use the wrapper's
ordinary `run macos27-provisioned --suspendable --no-clipboard` command; provisioning
options are needed for the first boot of a fresh restore, not to repeat account
creation on this configured guest.

The deep checkout exceeded macOS's Unix socket path limit for Guestweave live
snapshots. A short private storage alias and a restart with `--suspendable`
allowed the live snapshot to complete; VM files were not moved. Keep the private
wrapper and alias together. Guestweave PR #181's build/lint/schema checks pass;
its dependency-review check failed because of repository dependency-graph/Advanced
Security configuration, unrelated to its telemetry change.
When a test needs the user's observation, wait for the answer before continuing
or cleaning up. Silence and the withdrawn `q` replies are not test results.

The split-deployment regression now runs both shipped server roles against one
SQLite or PostgreSQL database and a shared keyring. Inventory is populated through
MDM responses; no inventory is copied to a second DDM database. Both backend
regressions verify version/capability gating and restart persistence. The server
module now depends on published library revision `c81508cf336c`, containing the
shared `osversion` API; standalone dependency resolution and installation pass.
These are automated results, not new native macOS acceptance.

Inspect `git status --short` before editing and keep private lab material ignored.
Retain macOS 26 automated coverage and both schema pins. Use `osversion` directly;
the old `support.Version` wrappers have been removed intentionally.
No fixture's `live: pending` is a claim of native acceptance.

```sh
sw_vers
git status --short
python3 test-lab/apple-features/check-host.py --phase after --workspace test-lab/local/certs/bench --out test-lab/local/apple27/evidence/after-host.json
go build -o test-lab/local/bin/dmserver ./server/cmd/dmserver
go build -o test-lab/local/bin/dmctl ./server/cmd/dmctl
```

The host check is read-only, refuses an unexpected major OS and never overwrites
an existing evidence file. Pick a new filename for a repeat run. Its output records
OS/build, architecture, Go version, checkout revision, schema provenance and fixture
hashes without collecting serial numbers, credentials or profile contents.

## Prepared implementation

- Per-enrollment DDM target resolution, dependency filtering and activation/status
  rewrites with matching tokens; no snapshot replacement on target lookup errors.
- Command/profile availability checks at enqueue and dispatch, including OpenID
  profile contents and legacy update query values. Signed profile bytes survive
  inspection. Generic and Apple Kerberos SSO schemas are disambiguated.
- Generated validation of empty top-level declarations and individual query names;
  generated asset paths preserve context even when Go nested types are reused.
- Read-only compatibility preview through admin API and `dmctl`.
- Opt-in HTTPS cache-report receiver, device-bound hashed credentials, rotation,
  revocation, 30-day default retention, pagination and common transactional storage
  across memory, SQLite, PostgreSQL and MySQL.
- Version/platform fixtures, preparation helper and thirteen required OS 27 test
  contracts. See the coverage matrix for feature boundaries and external needs.

The [reviewed inventory](../../test-lab/apple-features/macos27-coverage.json)
enumerates 50 source cases, 73 explicit boundaries, 32 named fixtures and reviewed
SSO value floors. Its required contract covers inherited metadata as well as
missing cases. New Accessibility, web-content-filter, sensitive-content Siri,
interactive-profile asset and legacy ManagedApp config fixtures are preparation
only until native results are recorded. The existing `website-privacy` fixture
already uses `SafariSettings.Privacy`; preserve its conclusive live observations.
`make test-schema-contracts` requires a passing execution of each named contract
and appends its coverage to `cover/unit`, so the ordinary coverage gate includes
the tagged OS 27 tests. Missing or skipped tests still fail the contract gate.
The complete remote test matrix and unchanged 95% coverage gate passed at
`eb5208a`; see [the CI run](https://github.com/deploymenttheory/go-apple-dm/actions/runs/35204383183).

The primary schema pin is `b0180185a5e4077070710033341b71d0cbe1a18a` and history is
`67045e2fa06f528b196c01edee6a8bf88b844beb`. `GENERATED_FROM.json` records both.
Regenerate with the existing generator; never hand-edit generated files.

## Evidence and retained macOS 26 baseline

`test-lab/local/apple27/evidence/before-host-final.json` records this host before upgrade.
[Preparation validation](macos27-prep-validation.md) records the completed checks
and evidence filenames. Automated cases explicitly exercise 26.0, 26.4, 26.6.2 and
27.0; existing mixed-fleet tests also retain older macOS coverage.

At preparation time `test-lab/local/bench.json` was absent, but the retained live
workspace is `test-lab/local/certs/bench`. Earlier physical macOS 26 results are
documented in [preparation validation](macos27-prep-validation.md#earlier-physical-macos-26-evidence).
The host baseline itself records host facts only. Inspect current lab state;
do not assume old experimental servers, ports or credentials are active. See
[Mac enrollment testing](../operations/mac-enrollment-testing.md) and
[bench operations](bench.md). Never discard existing keys or databases to get a
clean start.

The canonical server configuration is `test-lab/local/certs/managed/setup.json`;
it selects `managed/recovered-20260913/database.sqlite` and its retained keys.
Start the current workspace binary with `--setup-file` pointing to that file.
Use the nested bench workspace with `-attach-url https://127.0.0.1:8443` for live
scenarios. Do not start its old supervisor: it selects an earlier database.
The selected-feature checkpoint also protects encrypted FileVault recovery state;
never restore an earlier database over the current state to restart testing.

An upgraded host cannot produce new physical macOS 26 evidence. Retain the
completed earlier results with their exact scope and continue automated 26
coverage. A separate 26 device is needed only for additional physical mixed-fleet
checks. On a restricted execution environment, verify enrollment status again
outside that restriction before concluding that the device needs re-enrollment.

## Acceptance sequence

1. Confirm the host now runs 27 using the commands above. Read the before/after
   evidence and retain any differences in schema pins, fixture hashes and code.
2. Run `dmctl bench doctor -workspace test-lab/local/certs/bench`. For
   a fresh lab use the documented live enrollment workflow. Required inputs are
   a valid MDM APNs identity, device-reachable HTTPS, enrollment identity/trust,
   an authenticated admin context and a real device enrollment ID. User-channel
   tests additionally require a registered user channel. Do not invent these.
3. Run `LIVE-001` using the actual device ID. Require acknowledged tracked
   `DeviceInformation` with current `OSVersion` and `BuildVersion`. Read enrollment
   inventory back from the server; hardware version alone is insufficient for
   delivery gating. Then issue tracked `DeviceInformation` queries for
   `IsSupervised` and `IsAppleSilicon`: `LIVE-001` requests only OS/build and does
   not refresh those capabilities. Verify the compatibility target before using
   supervision-gated settings. `SecurityInfo.ManagementStatus` supplies DEP and
   user-approved observations when that command is permitted. Never infer these
   values from an operator-supplied enrollment profile. Preserve bootstrap-token
   state without printing its value.
4. Verify DDM bootstrap, declaration-items, individual declaration fetch and status
   reporting. Compare the compatibility preview to what the device actually
   fetches. Unknown inventory must bootstrap rather than receive advanced settings.
5. Run one selected feature bundle at a time using the procedure below. Follow its
   row in the coverage matrix for positive behavior, removal and dependencies.
   Acknowledging a declaration does not by itself prove an app, issuer or OS UI
   behavior. Record both protocol and observed behavior.
6. Run cache reporting and update management as detailed below. Keep the real OS
   install/deadline experiment separate from schema/preparation checks.
7. Exercise a retained 26 device if available: shared inventory/DDM update settings,
   URL-backed legacy profile and legacy update management must continue working;
   27-only settings must be withheld. Record this independently of 27 results.
8. Clean up only the test assignments and credentials. Verify removal/status and
   ordinary management connectivity. Update the validation record with exact
   build, revision, outcomes and private artifact paths. Mark unavailable external
   prerequisites **blocked**, never passed. Leave non-Mac platform acceptance
   pending until the corresponding hardware is tested.

## A single feature bundle

Use the existing authenticated CLI context. `dmctl` below means
`test-lab/local/bin/dmctl` with that context's server URL, admin credential and CA.
Replace `DEVICE-ID`, `FEATURE` and `LAB-SET` with real isolated lab values.

```sh
python3 test-lab/apple-features/prepare.py --feature FEATURE --out test-lab/local/apple27/FEATURE
```

Before upload, review every listed declaration in `bundle.json`. Substitute real
asset content, endpoints, identifiers and hashes in private copies; the committed
examples use deliberate placeholders. `--overrides PRIVATE.json` replaces entire
Payload objects and includes dependencies. Validation uploads have no device
effect until assigned, but assignments can immediately activate settings.

For each declaration file listed by the bundle, run:

```sh
dmctl declarations put -file test-lab/local/apple27/FEATURE/DECLARATION.json
dmctl sets add LAB-SET DECLARATION-IDENTIFIER
```

Then assign the reviewed set only to the lab enrollment:

```sh
dmctl sets assign device DEVICE-ID LAB-SET
dmctl enrollments compatibility device DEVICE-ID
dmctl notify
```

On macOS, `app-privacy` and `website-privacy` require the installing-user
channel. Select its actual enabled enrollment ID and supply the parent device:

```sh
dmctl sets assign user USER-ENROLLMENT-ID LAB-SET -parent DEVICE-ID
dmctl enrollments compatibility user USER-ENROLLMENT-ID -parent DEVICE-ID
dmctl notify
```

Assignments already schedule change notifications. The compatibility command is a
read-only explanation, not an assignment approval gate. Check target inventory
before assigning; do not rely on having time to review after a notification.
Collect declaration statuses, raw status reports, typed status values and errors
through the existing [status inspection commands](../operations/status-and-profile-inspection.md).
Save private captures alongside the before/after host evidence. For each case
record `pending`, `passed`, `failed`, `blocked` or `not-applicable`, the exact target,
expected/observed result, timestamps, cleanup and artifact paths. Do not record
credentials, AppleCare tokens or fetched secret assets in public documentation.

After each case:

```sh
dmctl sets unassign device DEVICE-ID LAB-SET
dmctl notify
```

For a user-channel case, use
`dmctl sets unassign user USER-ENROLLMENT-ID LAB-SET -parent DEVICE-ID` instead,
then notify and inspect that same user channel.

Verify removal before deleting that test set's members or declarations. Preserve
unrelated enrollment policies. The bundles share stable fixture identifiers, so
do not overlap different cases on the same server.

## Cache-report acceptance

Enable `DM_CONTENT_CACHE_URL` using the device-reachable HTTPS origin; ensure
reverse-proxy logs redact ingestion paths. Configure retention if different from
30 days. Generate a reporting credential using `content-cache rotate DEVICE-ID`;
put its returned private URL in the cache declaration's `ManagementStatusTarget`.
For the lab's private HTTPS CA, use `ManagementSecurityConfig: signedByCACert`
and a certificate asset named by `ManagementStatusCertificateReference`.
On build 26A428, native reporting cancelled TLS authentication when the shared
MDM listener requested client certificates. A dedicated HTTPS ingress that
does not request client certificates, with verified HTTPS forwarding to the
receiver, resolved this. Preserve MDM client authentication on its own listener.
Forward both POST and PUT: the native Mac sent POST even though the declaration
description says PUT; the library receiver accepts both. The lab's verified
reporting origin is `https://127.0.0.1:9443`, backed by the private fixture proxy.
Its TLS identity is loaded from the existing encrypted store.
Deploy the declaration and observe **a report sent by macOS**, not a curl-generated
substitute. Confirm it is associated with the authenticated enrollment and visible
through `content-cache reports DEVICE-ID`; test pagination. Restart the server
with its same database/storage key and verify persistence. Rotate, deploy the new
URL and verify the old URL is rejected; revoke and verify rejection again. Verify
cache status items separately from the out-of-band native report stream.
The report's `buildVersion` was the cache service build (`265`), not the Mac's OS
build. Use tracked MDM inventory for OS/build acceptance. Restore the cache's
initial activation state before removing the test: removing `AutoActivation`
alone does not deactivate it. For the initially disabled lab cache, apply
`DenyActivation: true`, verify native inactivity, then remove the declaration.

## Software-update acceptance

Start with settings only: deferrals, automatic actions, Background Security
Improvements, administrator authorization and notification policy. Select real
available update metadata through existing lookup helpers before testing a deadline.
The committed year-2099 fixture is a validation example, not an installation plan.
Use a future **device-local** `TargetLocalDateTime` with no `Z` or UTC offset.
The example `update-macos` payload enables automatic installation. Replace its
entire Payload in a private override for a settings-only pass: keep OS/security
installation `AlwaysOff`, omit beta enrollment and enforcement, and test download
controls separately. Administrator authorization uses `AllowStandardUserOSUpdates`.

Confirm bootstrap authorization prerequisites, capture progress/error status, test
an interruption and recovery, then verify the final OS/build through tracked
inventory. A real enforced update may close apps and reboot: coordinate that
specific target and time with the user. The earlier request authorizes the user's
upgrade followed by acceptance; it is not an instruction to erase the host, enforce
an additional unreviewed upgrade, or change its FileVault login policies blindly.
ADE minimum-OS and Setup Assistant cases need a spare/re-enrolled ADE device.

## Remaining external prerequisites

OpenID needs a compatible SSO extension and IdP account. Biometric/watch/guest
variants need appropriate hardware/accounts and recovery access. ManagedApp and
package cleanup need disposable signed app/package fixtures; do not use arbitrary
installed apps or real user files. The current fresh SCEP enrollment has access
rights 19 (profile inspection, profile installation/removal and device inventory).
It does not include the app-management right 4096. App takeover/install/removal
acceptance therefore needs a separately reviewed enrollment with that right; an
existing MDM payload cannot gain rights through an in-place update. The macOS 27
SDK includes ManagedApp, so SDK absence is not a blocker on this Mac. Enhanced diagnostics needs an AppleCare ticket
token and the Mac user channel. Networking and certificate cases need real test
endpoints/issuers. Assessment mode is an app-side SDK integration, not a new MDM
server endpoint. iOS/iPadOS, Shared iPad, tvOS, visionOS and watchOS need their own
devices; the Mac run cannot sign those off.

## Regression commands

```sh
go test -race -tags schema_seed_os_27 ./... ./server/...
python3 .github/scripts/schema_monitor.py contracts --output cover/schema-contracts
go run ./cmd/schemagen verify
python3 scripts/lint.py
python3 -m unittest discover -s .github/scripts -p '*_test.py'
python3 -m unittest discover -s test-lab/apple-features -p '*_test.py'
go test -race -tags e2e ./server/e2e/... ./server/acceptance/...
```

SQL persistence checks use `scripts/testdb.sh up`, the printed test-only DSNs, then
`go test -p 1 -race -tags integration ./server/statestore ./server/internal/app`.
Local listeners/database connections need the normal test execution permissions.
On this host the system `make` shim reports an unaccepted Xcode license; invoking
the documented scripts and Go commands directly allowed preparation checks. Do
not accept a license on the user's behalf. After the OS upgrade verify Xcode/CLT
and Go are usable before diagnosing a protocol failure.
