# macOS 27 live validation

**Scope correction — 18 September:** the acceptance target is the **Go library
and reference server**. Verify models, wire encoding, validation, compatibility,
MDM command delivery/results and DDM declaration/asset/status lifecycles. Use
representative physical macOS 27 exchanges to confirm interoperability. Apple
feature behavior (such as Siri responses, Calendar editing or consent-dialog
variants) is supplementary evidence, not a readiness gate. The earlier native
behavior checklist is no longer the continuation plan. See the
[project acceptance criteria](macos27-handoff.md#project-acceptance-criteria).
Recorded results retain their original revision and scope; this correction does
not convert incomplete native cases into passes or assert final project sign-off.

**Current guest continuation:** reliable APNs remains unresolved on **27.0 / 26A428**
and **27.2 beta / 26B5086k**, tested on a 27.0 host. Native provisioning, the Tart
comparison, Terminal launch and upgrades from a working 26 guest have not met
acceptance. The beta acknowledges one request after cold boot/login, but two
subsequent independent pushes time out with verified HTTPS. Native logs still
report APNs reference-key access failure (`-25308`). See the
[beta result](#macos-272-beta-result) and [upstream incident review](macos27-vm-incidents.md).
The 26 baseline passes native device/user enrollment, three timed pushes and
LIVE-003 / LIVE-004. Its working checkpoint, both original 40 GB guests and the
conclusive physical results below remain retained. The tested beta guest is
preserved and shut down; no declarations are assigned.

**Permission-test follow-up:** the user requested a repeat of the inconclusive
app and website permission tests only. Conclusive results below remain retained.
The fresh permission evidence is under
`test-lab/local/apple27/evidence/retest-20260916T212123222063Z/`.
The app uses a fresh signed bundle identity. Its initial headless status query
reported undetermined permissions, but a later headless query disagreed with the
foreground app's grant result; that probe is not accepted as evidence of the
GUI app's initial or current authorization. The user reported separate
permission prompts; the supplied screenshot shows the standard camera prompt
while the app is waiting for permission choices. The combined organization
prompt has not been demonstrated. The user confirmed granting both permissions,
and the foreground app recorded `cameraGranted=true` and `microphoneGranted=true`
at 21:30:25 UTC. The user then switched camera access off in Settings. At
21:37:38 UTC, the foreground app recorded `cameraGranted=false` and
`microphoneGranted=true` while the declaration was still valid and active. The
user confirmed that prompts appeared only on the first request, with none during
the subsequent checks while the policy was active. After policy removal, the
user supplied a screenshot of a new camera permission prompt at 22:41 local
time, then reported a microphone permission prompt after the camera-choice
instruction. At 21:45:00 UTC the foreground app recorded camera denied and
microphone granted, matching the user's final report. The app policy's removal
was verified again, and the disposable app was closed. The agent
did not reset permissions or replace the app between these checks; the cause
of the renewed prompt is not yet established.

Safari testing resumed on 17 September with a controlled `https://localhost:9443`
page. The user supplied Safari Websites settings screenshots showing `localhost`
set to Ask for both camera and microphone, and `127.0.0.1` set to Allow for both.
Both screenshots were retained under `website-baseline/`. Prior consent history
is explicitly unknown; Ask settings do not establish whether a site has prompted
before. After the localhost policy became valid and active on the installing-user
channel, displaying the page produced a combined camera/microphone consent prompt
with the exact organization justification. The agent selected Allow through the
authorized Safari UI and verified that both settings displayed
`Allow, Suggested by “go-apple-dm”`. At 03:35:53 UTC the page completed both requests
with `granted`, requiring no further consent choice. Changing only camera to Deny
was honored while the declaration remained valid and active: at 03:39:23 UTC,
camera returned `NotAllowedError` and microphone remained granted.

The unconfigured `127.0.0.1` comparison also showed organization attribution for
both permissions. That IP had been targeted in the earlier test, so this does
not establish site isolation or prove that the localhost rule affected another
host. Site isolation remains inconclusive. After verified policy removal, Safari
retained camera Deny, microphone Allow and the microphone suggestion attribution;
the 03:41:52 UTC request confirmed camera denied and microphone granted. The
attribution label alone therefore does not establish an active declaration.
Localhost was then restored to the supplied Ask/Ask baseline, and both channels
were checked for absence of test declarations and statuses. The repeat's
organization-Allow, grant and camera-revocation cases passed. Not Now and wildcard
matching were not exercised; the app combined-prompt result remains unproven.
The private
fixture uses the existing trusted TLS identity and preserves the earlier page
and evidence. See `website-baseline-prepared.json` in the repeat-run directory.
The user authorized the agent to drive Safari and observe its UI directly.
Each unanswered question is a stopping point: keep the test policy
assigned and await the answer before continuing or cleaning up. No earlier reply,
including the withdrawn `q` replies, counts as an observation for this repeat.

Run started 16 September 2026 on **macOS 27.0 (26A428)**, Apple silicon,
Go **1.27.1**. Base revision: `f84a831509fbc9d88f7810866a0b49fbe97b2d3e`, with the
prepared working-tree changes. **This Mac's current test pass is complete, with
failures and remaining acceptance recorded below.** Results apply only to the
cases and variants recorded here; fixture-wide live acceptance remains pending
where other platforms or behavior have not been observed.

Private run directory:
`test-lab/local/apple27/evidence/live-20260916T185210Z/`.
It contains the initial diff and source hashes, binary hashes, host evidence,
database/key checkpoint, exact scenario JSON/JUnit reports, raw private device
evidence, and per-feature preparation, application and removal records.
Credentials, profile grants, device identifiers and recovery material stay there.

## Verified results

| Check | Result | Evidence within the private run directory |
|---|---|---|
| Tracked inventory after upgrade (`LIVE-001`) | Passed: 27.0 / 26A428 | `live-001-20260916T185600293963Z/` |
| Retained SCEP and installing-user channel (`LIVE-003`) | Passed after upgrade | `live-003-20260916T185745702504Z/` |
| Fresh hardware-bound, attested ACME enrollment (`LIVE-002`) | Passed, including after restart with re-enrollment disabled | `live-002-20260916T195025320806Z/`, `live-002-20260916T195058613731Z/` |
| Fresh SCEP and installing-user channel (`LIVE-003`) | Passed after the scope-corrected binary restart and again after final feature cleanup | `live-003-20260916T195409230401Z/`, `live-003-20260916T203920102921Z/`, `live-003-20260916T210954223921Z/` |
| DDM inventory subscriptions, application and removal (`LIVE-004`) | Passed on retained and fresh SCEP enrollments | `live-004-20260916T185813076810Z/`, `live-004-20260916T195427021202Z/` |
| Tracked supervision and Apple silicon inventory | Passed; compatibility target now records supervision | `capability-inventory-*.json` |
| Legacy profile through a data asset | Passed installation, managed preference, ProfileList and removal after correcting the asset-server certificate | `feature-legacy-asset-20260916T200350600367Z/` |
| Legacy profile through its URL | Passed installation, managed preference, ProfileList and removal | `feature-legacy-url-20260916T200519970931Z/` |
| Siri AI restriction | Passed declaration status and native managed preference application/removal; AI request behavior was not exercised | `feature-siri-20260916T201055404741Z/` |
| Binary controls | Failed isolation: unrelated non-Apple executables also terminated; recovered by removing the test assignment | `feature-binary-controls-20260916T200703527400Z/` |
| Intelligence: Visual Intelligence and Calendar editing | Protocol valid/active and removed; requested behavior unconfirmed by readable managed preferences | `feature-intelligence-20260916T203056198244Z/` |
| Software update settings only | Protocol valid/active and removed; effective override unconfirmed by readable preferences; no update initiated | `feature-update-macos-20260916T203238514823Z/` |
| Native content-cache reporting and certificate asset | Passed activation, configured limits, HTTPS POST ingestion, authenticated device association, pagination, restart, credential rotation/revocation, and restoration to disabled | `feature-content-cache-20260916T203920052248Z/` |
| Scoped encrypted DNS | Protocol valid/active; native lookup timed out with both address configurations; connection attempts failed before reaching the local DoH handler; removed and original unresolved behavior restored | `feature-network.dns-settings-20260916T205333041804Z/` |
| Separate external-intelligence restriction | Passed native application/removal: `allowExternalIntelligenceIntegrations=false` in system/user managed preferences, then absent after removal | `feature-external-intelligence-20260916T205759710237Z/` |
| App privacy defaults | Valid/active on installing-user channel; native app recorded camera and microphone grants, confirmed by the user; test declaration removed; exact prompt unconfirmed | `feature-app-privacy-20260916T205914303439Z/` |
| Website privacy defaults | Valid/active on installing-user channel for the controlled HTTPS origin; user reported temporary permission grants in Safari; test declaration removed; exact prompt and origin isolation unconfirmed | `feature-website-privacy-20260916T210701277499Z/` |
| Final cleanup and retained management | Passed: no test declarations/statuses on either channel, cache disabled, cache credential rejected, SCEP user-approved enrollment retained, FileVault on, re-enrollment disabled | `final-cleanup-audit-20260916T211039587005Z.json` |
| Unit/race suites, both modules with OS 27 tag | Passed; Go cache reuse is identified in the log | `unit-race.log` |
| Twelve required OS 27 contracts | Passed; none missing | `schema-contracts/result.json` |
| Generator verification, lint, schema-monitor and fixture Python suites | Passed | `regression-results.json` and corresponding logs |
| Embedded E2E and process acceptance | Passed | `e2e-embedded.log`, `acceptance-process.log` |
| PostgreSQL/MySQL state and app integration | Passed with `-count=1 -p 1 -race -tags integration` | `sql-result.json`, `sql.log` |

## Findings and corrections

- Native cache uploads initially cancelled during the client-certificate
  challenge on the MDM listener. A separate HTTPS ingress using the same managed
  server identity and verified HTTPS upstream resolved this. The fixture proxy
  then needed POST forwarding as well as PUT; the production receiver already
  supports both. Native reports received HTTP 202. Fresh `content-cache.info` and
  `content-cache.status` were observed; older parent/peer rows were not counted as
  new evidence. The report build `265` identifies the cache service; OS build
  acceptance remains the tracked MDM result.
- Live-fixture review exposed unenforced declaration `allowed-scopes` and
  `allowed-enrollments` metadata. Support checks now enforce these for explicit
  MDM channels, including Shared iPad overrides. The generator preserves empty
  lists as explicit prohibitions. App/website privacy Mac fixtures now declare
  their required user channel; the private app identifier includes its signed
  designated requirement. Regression coverage verifies both valid delivery and
  withholding on incompatible channels/enrollment contexts.
- DDM simulator acceptance cases now obtain supervision through tracked
  inventory before expecting a Mac management-test declaration. One schema test
  also needed its required supervised target made explicit. Full unit/race,
  embedded E2E, process acceptance, lint, fixture tests, twelve contracts and
  generator verification passed after these corrections. See
  `regression-20260916T203920Z/`, `regression-20260916T203853Z/` and the successful
  non-unit checks in `regression-20260916T203041Z/`. Earlier failed attempts are
  retained. A private nested module keeps disposable lab source out of the
  repository package scan.
- While `AllowSiriAI: false` was valid and active, both
  `/Library/Managed Preferences/com.apple.ironwood.support.plist` and its
  installing-user counterpart contained `allowSiri3489: false`. Both files
  disappeared after the test declaration was removed. Private before/after
  records establish this mapping on build 26A428; the internal preference name
  is not a public API contract. Siri and ChatGPT settings remained visible, and
  the ChatGPT extension remained enabled. External intelligence has a separate
  declaration, which was not assigned during the Siri case. A later separate test
  verified its managed preference application/removal. Screenshots alone were
  inconclusive for Siri AI.
- The disposable binary deny rule became valid and active, but Python and
  `dmctl` also terminated with exit 137. Native Apple tools and the already
  running server remained available, allowing the assignment to be removed.
  Python and the disposable binary then ran successfully; declaration cleanup
  completed. Do not repeat this rule on the working Mac. The captured payload
  and status evidence do not yet establish whether the cause is OS behavior or
  payload semantics. Binary-control acceptance remains failed.
- Restricted execution reported the Mac as unenrolled and hid its signing
  identity. Unrestricted native inspection found the retained user-approved
  enrollment, which then passed inventory and both channels. An unavailable
  restricted view must not be treated as a negative device fact.
- The canonical setup uses the recovered database and current certificate
  authorities. The nested bench's old supervisor selects an older database.
  The maintained CLI attached to the current server throughout this run.
- `LIVE-001` refreshes OS/build only. Separate tracked `DeviceInformation`
  queries for `IsSupervised` and `IsAppleSilicon` were needed before evaluating
  supervision-gated feature declarations. Unknown capability flags were not
  replaced with operator assertions.
- The first legacy-asset attempt failed with `Error.AssetCannotBeDownloaded`
  and a native TLS error. Its old asset-server identity passed a local trust
  probe but failed the device fetch. Serving the same asset with the **current
  managed HTTPS identity** resolved the failure. Its key stays in the encrypted
  store; the private fixture server loads it through the normal runtime API.
  The failed attempt and cleanup remain in
  `feature-legacy-asset-20260916T195446942847Z/`.
- The handoff now names `ManagementStatusTarget` and
  `AllowStandardUserOSUpdates`, matching the pinned schema. The host helper accepts
  the actual bench workspace. Earlier physical macOS 26 results are retained in
  [preparation validation](macos27-prep-validation.md#earlier-physical-macos-26-evidence).
- Xcode's licence remains unaccepted. Go builds work, and the installed Command
  Line Tools SDK builds the disposable consent app when its SDK path is explicit.
  No licence was accepted by the agent.

## Final state and remaining acceptance

No test feature remains assigned. All 12 feature application/removal attempts,
including the failed first legacy-asset attempt, have removal records. Final
inspection found no acceptance declarations or statuses on either management
channel, inactive content caching, and HTTP 401 for the revoked cache credential.
Fresh SCEP remains enrolled and user-approved; `LIVE-003` passed again after
cleanup. FileVault remains on and re-enrollment is disabled. The canonical server
and private HTTPS fixture remain available for follow-up; earlier databases and
macOS 26 evidence were preserved.

The disposable app recorded camera and microphone grants at 21:02:37 UTC without
capturing media. The user subsequently reported temporary Safari grants. Both
privacy declarations were removed. The disposable app was closed, and
`tccutil reset` succeeded for its camera and microphone decisions only; see
`consent-fixture-cleanup-*.json`. Safari's user choices were not reset. These
observations establish grants during the tests, not that the declarations caused
a particular prompt or that policy removal itself revoked the grants.

| Remaining case or variant | Limitation |
|---|---|
| App consent | Repeat confirmed separate standard prompts, grants and camera revocation with microphone retained. Combined organization prompt remains unproven; the cause of renewed prompts after removal is unresolved |
| Website consent | Repeat confirmed the organization prompt, Allow, grants and camera revocation. Origin isolation remains inconclusive because the comparison origin had prior consent history; Not Now and wildcard behavior remain untested |
| Visual Intelligence, Calendar natural-language editing and effective update controls | Protocol results retained; requested behavior not confirmed by readable preferences |
| Encrypted DNS | Local endpoint connection failure retained; needs a compatible native DoH endpoint before behavior acceptance |
| ManagedApp SDK and package takeover/install/removal | Current enrollment rights 19 exclude app-management right 4096; needs separately reviewed enrollment rights and matching disposable app/package fixtures. The ManagedApp SDK is present |
| Platform/extensible SSO | No reviewed provider extension and IdP test account |
| DNS proxy and VPN plug-in | No disposable signed provider extension and test service |
| Relay and IKEv2/IPsec | No reviewed relay/VPN endpoint and credentials |
| Enhanced diagnostics | No AppleCare ticket token |
| Additional ACME/SCEP credential assets and password delivery into apps | Enrollment identity flows passed, but these distinct consumer/issuer cases have not been exercised |

Automatic OS installation, enforced deadlines, ADE/Setup Assistant, other Apple
platforms and additional physical macOS 26 testing are outside this pass. The
original standalone dependency limitation was resolved in the repository follow-up below.


## Repository follow-up — 17 September 2026

In PR #64 (merged on 17 September), the old container split
test's independent stores were replaced with two shipped roles sharing one
persistent database and compatible encryption keys. The PostgreSQL matrix now
uses PostgreSQL for both roles instead of a separate SQLite DDM container. The
application split test also uses shared SQL and normal enrollment; its manual
inventory import was removed. Assignments follow enrollment, whose lifecycle
correctly clears prior DDM state.

Both backend split regressions passed with `-race -count=1`: tracked inventory
is visible across roles, missing inventory and macOS 26 withhold the macOS 27
fixture, macOS 27 permits it, loss of supervision withholds it, and restored
supervision permits it again. Both roles retain state across restart. The test
refreshes Docker's ephemeral published ports after restart. An earlier PostgreSQL
fixture process failure required recovery; it was not counted as a pass.

The complete SQLite and PostgreSQL E2E suites passed, as did shared acceptance,
affected application/adapter/synchronization unit suites, repository verification
(including 51 schema-monitor tests), server lint and tagged-test compilation.
Standalone dependency resolution, public-package builds, all-server builds and
both CLI installations passed with `GOWORK=off`, no replacements, and the exact
declared library version `v0.7.4-0.20260917043416-c81508cf336c`.

The user has now authorized continuation beyond the original physical pass,
including a visible Guestweave macOS 27 VM. Outstanding live cases remain pending;
the repository checks above do not establish native feature behavior.

SQLite CI subsequently exposed a shutdown race: `database/sql` can finish its
automatic rollback before `Commit` observes cancellation, returning `ErrTxDone`.
The transaction coordinator now preserves that error and includes the context's
cancellation cause. A deterministic regression waits for rollback before commit,
verifies that writes and commit notifications are discarded, and confirms that an
explicit rollback without cancellation still reports a transaction error. The
transaction, maintenance and application race suites passed; the affected CLI E2E
test passed ten consecutive runs with `-race -count=10`.

The shared-database architecture diagram was regenerated from its source JSON.
All nine artifact checks and the repository's browser interaction checks passed.
Archify's separate one-screen check reports vertical overflow, as expected under
the repository's documented scrolling reading profile; it is not recorded as a
pass. Browser measurements found no horizontal overflow or chrome overlap.

Guestweave was built and signed locally using the installed Command Line Tools.
A schema-version mismatch in its OpenTelemetry resource initialization was fixed
and merged as [Guestweave PR #181](https://github.com/deploymenttheory/guestweave-cli-macos/pull/181).
Its telemetry race test, vet, lint and restore-image lookup passed; the signed
CLI was rebuilt from the submitted commit. Apple returned
`UniversalMac_27.0_26A428_Restore.ipsw`, 26,626,436,228 bytes. The user authorized
clearing the Go build cache and obsolete goimports indexes, recovering about
60 GiB. Referenced/recent indexes, downloaded modules, tools and lab data were
retained. The requested guest disk limit is **40 GB**. The initial 48 GB creation
attempt was cancelled during download, before any guest disk was created; the
partial restore image was retained and resumed. The completed image matches
Apple's SHA-256 checksum. Guestweave successfully created `macos27-acceptance`
with a disk verified as 40,000,000,000 bytes, four virtual CPUs and 4 GiB RAM.
The guest booted to Setup Assistant in a visible native window and obtained a
network address. The completed restore download was removed after that boot,
retaining its checksum record and recovering about 25 GiB. A concurrent Go build
later regrew the build cache to 49 GiB and filled the host filesystem. Repeating
the previously authorized build-cache cleanup recovered about 53 GiB; other
active work was not terminated. The guest result is recorded below.

The expanded macOS 27 inventory names 50 source cases, 73 explicit version
boundaries and 32 fixtures, plus 12 reviewed SSO value floors. The required
inventory contract also compares inherited generated support at 663 paths across
versions and enrollment contexts. All thirteen named OS 27 contracts passed,
including fixture completeness and the additional supervision/channel withholding
cases. Accessibility, web-content-filter, sensitive-content Siri,
interactive-profile assets and legacy ManagedApp configuration now have prepared
fixtures; their live outcomes remain pending. Existing Safari privacy results
are unchanged.

Routine contract execution now contributes Go coverage to the unit-test artifact
on Unix and Windows, while still requiring explicit passing evidence for every
named contract. Generated conformance checks now verify nil-receiver validation,
command response schema paths and DDM declaration kinds. Schema and generator
race suites passed. Additional server tests passed for trusted-proxy HTTPS
assertions, rejected content-cache URL variants, disabled enrollment credentials,
invalid admin requests and retention settings. Managed OTA and revocation routes
also reject requests when their current issuer trust becomes unavailable.
Both module lint checks and tagged compilation passed with zero issues;
regeneration verification, 52 schema-monitor tests and four fixture-helper tests
passed. At revision `eb5208a`, the complete remote test matrix passed on macOS,
Ubuntu and Windows, including storage integration, both E2E backends, reference
server acceptance, standalone server installation and the thirteen required
OS 27 contracts. The combined coverage gate passed at 95.00% against its unchanged
95% minimum. See [the CI run](https://github.com/deploymenttheory/go-apple-dm/actions/runs/35204383183).
These automated results do not establish native feature behavior.

## Readiness follow-up — 18 September 2026

### Library and reference-server completion checks

The current tree was committed and pushed to PR #65 as `cddf78c` before completing
the corrected test scope. Follow-up changes add these repository-owned checks:

| Check | Evidence and acceptance boundary |
|---|---|
| Binary validation through HTTP | Eleven cases verify rejected creates/replacements, preservation of stored declarations, snapshots/tokens and pending notifications, and delivery of valid identifiers/qualifiers. Local race run passes. |
| Complete fixture payload fidelity | The existing required feature-delivery contract now compares the authored and served declaration after canonical JSON normalization, excluding only the generated server token. The full platform/version fixture matrix passes; token and withholding checks remain. |
| Standalone server runtime | The server dependency advances to `v0.7.4-0.20260918023638-cddf78c3196a`. Independent installation verifies binary module metadata and runs process acceptance against installed executables, including combined/split deployment, invalid replacements, restart persistence and CLI lifecycle. Local installation/runtime verification passes. |
| Final regressions | Affected schema, DDM, generator, server application/service/adapter race suites, all thirteen OS 27 contracts, `make verify` and affected-package lint pass locally. Full candidate CI and the 95% gate are tracked on [PR #65](https://github.com/deploymenttheory/go-apple-dm/pull/65). |

The focused process test also ran against the retained pre-fix server in isolated
simulated workspaces. Both combined and split deployments failed as expected:
the invalid SigningID-only allow rule returned HTTP 200 instead of 400. The
corrected standalone binaries pass the same test. The test never launches the
denied fixture binary or changes a physical Mac's policy. Existing native results
are retained; these new results prove library/server contracts, not Apple's UI
or enforcement behavior. The earlier full CI result at `eb5208a` remains historical.

### Retained native follow-up

The user clarified that readiness concerns the library and reference server,
not certification of Apple's feature implementations. Earlier test-placement
permissions remain recorded in the [handoff](macos27-handoff.md#approved-test-placement),
but do not make all those tests necessary. Stop the UI/feature-behavior campaign;
close demonstrated repository defects and audit the project acceptance evidence.
Missing AppleCare credentials, SSO providers or a functioning 27 VM do not alone
block project readiness. Any resulting untested native integration is disclosed.

**Binary identifier validation:** the generated checks accepted a path or signing
state alone for an allow rule, and a path alone for a deny rule. The pinned
Apple schema requires a nonempty CDHash or TeamID for allow rules; deny rules
also permit SigningID. The generator now enforces these identities while
retaining optional qualifiers. Fourteen invalid combinations reproduced the
gap before correction. All 28 new allow/deny regression cases pass after
regeneration, as do affected schema/generator race tests, all thirteen OS 27
contracts, generation verification and affected-package lint. Neither schema
pin changed. These are local results; the previously recorded complete CI matrix
and 95% gate remain tied to `eb5208a`.

The saved failing native binary fixture already contained a 40-character CDHash
and SigningID and did not contain an allow list. The validator correction does
not establish the cause of that native failure or convert it to a pass.

**Scoped encrypted DNS: passed on the standard HTTPS port.** The Mac was verified
as the retained physical 27.0 / 26A428 enrollment, with no assigned declarations.
Fresh tracked inventory confirmed supervision and Apple silicon. Native
URLSession HTTPS requests to the existing test endpoint succeeded. A declaration
limited to `macos27.invalid` became valid and active on port 9443, but native
resolution timed out and the responder received no matching query. After verified
removal, the baseline returned to unresolved.

A disposable loopback relay exposed the same TLS endpoint on port 443; it dropped
administrator privileges immediately after binding. With only the declaration's
URL port changed, native resolution returned the expected loopback addresses,
and the responder recorded the matching A and AAAA requests. Declaration removal
again restored the unresolved baseline. The relay was stopped and zero test
assignments were verified. Normal DNS settings and the retained MDM enrollment
were preserved. This establishes scoped native HTTPS DNS application, resolution
and removal on this build; it does not explain every custom-port failure or
cover all DNS declaration variants.

Private evidence is under `test-lab/local/apple27/evidence/readiness-20260918/`:
`schema-contracts/result.json`, `test-placement.json`, `physical-run.json` and
the selected physical run's `endpoint-*`, `dns-*`, `feature-network.dns-settings-*`
and `inspection-*` records. The earlier failed DNS run remains retained.

**Interactive profile delivery:** the data-asset-backed configuration and its
activation were reported valid and active. The data asset remained active with
validity unknown before any user-triggered fetch. An older URL-backed control
also became valid and active. Device Management did not expose the optional
profile in the observed pane, so presentation, acceptance/decline and update were
not established. Both test assignments were removed; a fresh `ProfileList` and
the native marker-preference check confirmed that the disposable profile was not
installed. Evidence is in `interactive-outcome.json` and the two
`feature-legacy-interactive-*` directories within the selected physical run.
This demonstrates configuration delivery and status handling, not completed
interactive asset retrieval or Apple UI behavior.

At the scope correction, inspection confirmed zero physical-device test
assignments and the current fixture marked removed. The update-settings UI was
opened for observation only; no new update policy or OS installation was applied.

## Guest continuation — 17 September 2026

The visible `macos27-acceptance` guest reports macOS **27.0 (26A428)**,
`VirtualMac2,1`, four virtual CPUs and 4 GiB RAM. Its disk remains exactly
40,000,000,000 bytes. Setup is complete; no Apple Account is signed in. A private SSH
key provides guest access; loopback forwards to ports 8443 and 9443 preserve the
lab's HTTPS origins and certificate verification. Guest FileVault was off at
the pre-enrollment baseline. The working physical Mac's FileVault and enrollment
were not changed.

The current server and CLI were built from `eb5208a` and promoted using the
canonical recovered database and retained keys. Existing admission correctly
rejected the unfamiliar guest. After recording its hardware UUID and serial,
only that exact pair was added to the admission policy. The server restarted
with re-enrollment still disabled. The issued SCEP profile was reviewed for its
identity payload, included trust, installing-user scope and rights **4115**
(the existing inventory/profile rights plus application management). The user
installed it. Settings and `profiles status -type enrollment` both show
user-approved MDM enrollment.

**Result: blocked before command/DDM acceptance.** The server received native
Authenticate at 10:23:10 UTC, but no TokenUpdate. Its enrollment therefore remains
disabled, with zero installing-user channels and zero assigned declarations.
The tracked inventory helper stopped at that prerequisite; it did not queue a
command or claim that Authenticate inventory was tracked DeviceInformation.

Guest logs show `apsd` failing to create its Secure Enclave reference key with
`NSOSStatusErrorDomain -25308` / `errSecInteractionNotAllowed`, followed by
`APSBAAClientIdentityProvider` failing to obtain its BAA certificate. The same
sequence occurred before enrollment and after console login. The guest's APNs
connections remain absent despite successful TCP checks to Apple's push service
on port 5223 and activation service on port 443. The guest clock was correct and
its HTTPS request to the lab readiness endpoint passed with the private CA.
This local evidence matches an
[existing macOS 27 VM report](https://developer.apple.com/forums/thread/840500),
including follow-up on a release-candidate host and guest. Apple DTS requested
guest diagnostics there; the thread does not establish a released fix.

The private evidence is under `test-lab/local/apple27/guestweave/`, including
`baseline.json`, `profile-review.json`, admission backup/change records,
`guest-enroll-003.png`, `enrollment-native-log-*.json` and
`enrollment-blocker-*.json`. No raw identities, credentials or logs are published.
`clean-install` preserves the pre-enrollment disk. After resolving the lab's
Unix socket path length with a short storage alias and restarting with
`--suspendable`, the live `enrollment-apns-blocked` snapshot completed and resumed.
The original guest's enrollment and diagnostic evidence remain preserved; no
feature policy is assigned. Guest-dependent binary, package/app and permission
variants remain blocked at transport readiness. Retained physical-device passes
and failures are unchanged.

### Native provisioning retry — Guestweave v1.1.0

[Guestweave PR #182](https://github.com/deploymenttheory/guestweave-cli-macos/pull/182)
added the missing native `VZMacGuestProvisioningOptions` path and was merged into
[v1.1.0](https://github.com/deploymenttheory/guestweave-cli-macos/releases/tag/v1.1.0).
The retry used a locally built and signed binary from release revision
`459299f6705bf721df1dceee67546f63d3b5c5d0`, with
`go-bindings-macosplatform` **v0.20.0** and `purego` **v0.11.0**.
The native provisioning integration test, focused package tests and changed-line
lint passed on the implementation commit; the release adds only changelog changes
to that code. These automated checks do not establish APNs success.

A separate `macos27-provisioned` guest was restored from the checksum-verified
27.0 / 26A428 IPSW. It has a new machine identity, an exactly 40,000,000,000-byte
disk, four CPUs and 4 GiB RAM. The first normal boot used `--provisioning-opts`
in a visible native window. Provisioning created the requested administrator,
logged in automatically and enabled SSH: authentication succeeded without manual
account setup or a guest-side script creating the account. The pre-enrollment
baseline confirmed that no MDM profile was installed.

Only this new guest's exact UUID/serial pair was added to the existing admission
policy. The original guest, its snapshots, the physical Mac's enrollment and the
canonical recovered database/keyring were retained. A fresh SCEP profile with
installing-user scope, included lab trust and rights **4115** was installed through
the guest's visible UI. Native `profiles` reports user-approved enrollment.

**Result: provisioning passed; APNs enrollment remains blocked.** Authenticate
arrived at **12:46:40 UTC on 17 September 2026**, but TokenUpdate did not. The server
retains `Enabled=false`, an unset `TokenUpdatedAt`, zero installing-user channels
and zero assigned declarations. No tracked command or feature declaration was
sent to this disabled enrollment.

The same `apsd` failure occurs before enrollment, after enrollment and after an
orderly guest reboot: Secure Enclave reference-key creation fails with **-25308**
(`errSecInteractionNotAllowed`), followed by failure to obtain a BAA certificate.
The 12:41 UTC pre-enrollment baseline still had **6.15 GiB available**; its
following log capture already contains 24 key-generation failures and eight
BAA-certificate failures. The later nearly-full Tart filesystem therefore does
not account for the initial observation.
The post-reboot capture, filtered to the new boot UUID, contains 24 key-generation
failure messages and eight BAA-certificate failures, with no logged host-VM
signed-nonce success. Verified lab HTTPS readiness and TCP connectivity to Apple's
push service on 5223 and activation service on 443 still pass after reboot.
Adding native provisioning did **not** restore APNs on this host/guest build;
the exact underlying platform cause remains unresolved.

Private evidence is under
`test-lab/local/apple27/guestweave/provisioned-lab/`: release/binary and restore
manifests, `baseline.json`, admission/profile review records, guest screenshots,
the three APNs log captures and summaries, and `verification-*.json`.
Credentials and raw identifiers remain private. The original guest's evidence
is unchanged. `restored-before-first-boot` preserves the fresh restore, and
`provisioned-enrollment-apns-blocked` preserves the configured guest's disk after
orderly shutdown. A live snapshot was refused by the free-space guard; the latter
checkpoint contains no RAM state. The new guest was reopened in its native window;
the original comparison guest is suspended. This retry supplies no new native
feature pass and does not change the retained physical-device results.

### Controlled Tart comparison

The provisioned 27.0 / 26A428 guest was shut down cleanly and its disk and NVRAM
were copied with APFS `clonefile` into an isolated Tart home. The copied machine
identifier, hardware model and MAC address were retained; the source guest stayed
stopped during the comparison. Both original 40,000,000,000-byte guest disks and
their checkpoints remain preserved.

The comparison used the official **Tart 2.37.0** release. Its archive checksum and
vendor signature were verified; the application was not re-signed. The same guest
was cold-booted first with Tart's suspendable device configuration, then with its
ordinary device configuration, which includes additional entropy/audio/input
devices. Both runs reproduced the BAA identity failure. The final capture,
restricted to that boot UUID, contains 36 key-generation failures and 12
BAA-certificate failures, with no host-VM signed-nonce success or connected APNs
interface. The enrollment remains disabled, with no TokenUpdate, installing-user
channel or assigned declaration.

This controls for the VM runner and its normal versus suspendable device setup;
it is not a fresh installation under Tart. The guest reports `VirtualMac2,1` and
`kern.hv_vmm_present=1`. Its 40 GB filesystem is also nearly full (293 MiB available
in the final capture), so that resource constraint remains a separate diagnostic
limitation. No Guestweave-specific APNs fix is established by this comparison.

Private evidence is in
`test-lab/local/apple27/guestweave/apns-recovery-20260917/`, including the
`guestweave-baseline-*`, `tart-baseline-*`, `tart-standard-devices-*` and
`tart-details-*` directories. The comparison guest was subsequently shut down
cleanly. Its disposable cloned disk was later reclaimed with approval to recover
upgrade headroom; the comparison configuration, NVRAM and diagnostic logs remain
retained. The original provisioned guest remains stopped and the earlier
acceptance guest remains suspended.

### macOS 26 fallback baseline

A separate macOS **26.6.2 / 25G83** guest was restored with Guestweave v1.1.0 on
the same macOS 27 host. The user authorized an 80 GB sparse disk for this fallback
only; the existing guests retain their 40 GB limit. The Apple-hosted restore image
was 19,772,231,540 bytes and its SHA-256 matched
`885503b7f4b06609e9a512f2befd40f59730640a3f1233e3892d60affdd51c95`.
Host storage was checked before downloading and restoring it. Normal Setup
Assistant was completed: Apple's SDK explicitly requires a macOS 27 guest for
`VZMacGuestProvisioningOptions`, so those options were not used on 26.

**Result: the macOS 26 baseline passes native APNs, enrollment and DDM.** Before
enrollment, `apsd` obtained its BAA certificate, signed the nonce using the host VM
identity and established a push connection. The capture contains no key-generation
or BAA-certificate failures. Only the new guest's exact UUID/serial was added to
admission. Its reviewed SCEP profile uses installing-user scope and rights 4115;
device and installing-user TokenUpdate arrived through the normal lifecycle.
The device's TokenUpdate timestamp is **14:52:56 UTC on 17 September 2026**.

Three separately queued and pushed DeviceInformation commands were acknowledged
in **1.50, 1.21 and 3.20 seconds**, each within the existing 45-second deadline.
The third followed an orderly shutdown, cold boot and login. Tracked responses
confirm 26.6.2 / 25G83, supervision and Apple silicon. Current-boot native logs
again show a successful host-VM signed nonce and no BAA/key-generation failures.
Both **LIVE-003** (SCEP device/user enrollment and APNs inventory) and **LIVE-004**
(DDM status subscriptions and temporary declaration removal) passed. The final
inspection retains enabled device/user channels and zero assigned declarations.
The guest was then shut down cleanly and the offline `macos26-apns-working`
checkpoint was created. Its first attempt had failed the host free-space guard;
temporary guest files were reclaimed during the cold boot, and the later attempt
passed that guard without changing its threshold. The validated fallback was subsequently restarted for the upgrade attempt.

Private evidence is in `apns-recovery-20260917/fallback-lab/` beneath the guest lab:
`macos26-baseline-summary.json`, `before-enrollment-*`, `after-cold-boot-*`,
`tracked-inventory-*`, `live-003-*`, `live-004-*` and `status-*`. Credentials,
identifiers, push tokens and raw protocol evidence remain private.

These macOS 26 passes establish a working control on the current host. The
same-identity upgrade comparison below tests whether that working APNs state
survives macOS 27.

### Same-identity upgrade to macOS 27

The fallback completed Apple's native in-place upgrade to **27.0 / 26A428** on
17 September. Its hardware UUID, serial, installing-user GUID and user-approved
MDM enrollment were retained. The disk remains exactly **80,000,000,000 bytes**;
the offline `macos26-apns-working` checkpoint remains intact.

The Apple-hosted full installer was **18,400,314,350 bytes**, with SHA-256
`e74aa9c2b31d0d764050874aabc4e761e12ef21e615dababf1bb2527a44cf7f8`.
Apple's package signature, all five XAR member checksums and embedded target
version/build metadata were verified. To satisfy the guest's storage preflight,
verified installer media was shared read-only from the host; its program and
frameworks ran locally in the guest. No erase or reduced-security options were
used. An initial preparation was interrupted by an unsuitable host-side
process-pause guard; that guard was retired, the guest recovered to 26, and native
APNs was checked again before the successful installation attempt.

**Result: upgrading an enrolled, working 26 guest did not restore APNs on 27.**

| Check | Result |
|---|---|
| Native OS and enrollment after upgrade and cold boot | 27.0 / 26A428; original VM/user identities and user-approved profile retained |
| Guest-to-MDM HTTPS | Verified certificate trust and `/readyz` before and after each conclusive push test |
| Apple network reachability | TCP 5223 to the push courier and TCP 443 to activation pass |
| New inventory command after tunnel correction | No acknowledgement within 45 seconds; recorded elapsed 45.71 seconds |
| New inventory command after full shutdown, cold boot and login | No acknowledgement within 45 seconds; recorded elapsed 45.56 seconds |
| Current-boot APNs logs | BAA certificate acquisition fails; no connected APNs interface or successful host-VM signed nonce in the capture |
| Server lifecycle | Device and one user channel retain their prior enabled state; device TokenUpdate remains the 26 baseline timestamp, 14:52:56 UTC |
| DDM assignments | Zero; 27 LIVE-003 / LIVE-004 were not attempted after the readiness failure |

The first two post-upgrade command timeouts were inconclusive because the lab's
SSH reverse tunnel had expired after ten idle minutes. The tunnel now uses
`ControlPersist=yes`, keepalives and forward-failure checking. The two subsequent
tests above verified HTTPS before and after delivery attempts, so the expired
tunnel does not explain those failures. SSH carries only the lab's MDM HTTPS
transport; APNs delivery uses the guest's native Apple connection.

Unlike the fresh 27 guests' key-generation failure, the upgraded guest's
`mobileactivationd` reports failure to query the existing reference key
`com.apple.apsd/apsd-rk-scrt`, with keychain error **-25308**. Retaining an old
TokenUpdate and enabled enrollment does not prove current push delivery. The
guest has about **39 GiB free**, so the nearly-full Tart comparison is not the
only failing 27 test. These observations locate the failure in the native
identity path; they do not establish the precise Apple implementation defect.

Private evidence under `apns-recovery-20260917/fallback-lab/` includes
`upgrade-complete-*`, `upgrade-transport-health-*`, `post-tunnel-retry-*`,
`coldboot27-*`, and tracked inventory directories ending in
`172122163626Z` and `172344564625Z`. Raw identities, keys and push credentials
remain excluded from the repository.

A subsequent queue audit retained **late acknowledged results** for all four
earlier post-upgrade requests. They completed together at **17:25:22 UTC**,
after their recorded deadlines. Their native responses report 27.0 / 26A428,
supervision and Apple silicon. This establishes tracked inventory, but not the
required timely response to each independent push; the trigger for that later
check-in was not established.

The approved additional Go temporary-directory and Windows ISO cleanup reclaimed
**22.11 GiB**. The completed 27 installer was subsequently removed after the guest
had shut down and released its read-only share, leaving about **22.4 GiB** free on
the host. Both original 40 GB disks and all retained checkpoints are preserved.

### UTM/Tart incident review and Terminal launch comparison

The [upstream incident review](macos27-vm-incidents.md) covers open and closed
UTM/Tart reports, linked patches and applicability to this lab. The current APNs
reports remain open, including Tart failure with provisioning enabled and on
27 RC. Older closed `-25308` reports concern the host Virtualization process's
GUI login/keychain context, rather than a booted guest's APNs reference key.

The fallback was therefore cold-booted directly from a new host **Terminal.app**
session as the graphically logged-in, non-root VM owner. Guest inspection also
confirmed an APFS **Recovery** volume, addressing the older stripped-image lead.
After confirmed guest console login, a fresh inventory request still timed out
at **45.77 seconds**, with verified HTTPS before and after. Native current-boot
logs contained ten BAA acquisition failures, no acquired BAA certificate and no
connected APNs interface. Terminal launch did not satisfy APNs acceptance.

A separate pre-login request timed out at 45.69 seconds, then acknowledged at
18:46:50 UTC during guest login. Its late response also confirms 27.0 tracked
inventory; it is not a timely-push pass. The post-login request remained pending.
The queue audit saved all five late results and cleared only that final expired
pending request, verifying that completed results were unchanged. Private evidence
includes `terminal-*`, `terminal27-*`, `terminal27-logged-in-*`,
`failed27-late-results-*` and `failed27-command-cleanup-*`.

### macOS 27.2 beta result

Apple lists **27.2 beta / 26B5086k**, released 16 September, but its
[release notes](https://developer.apple.com/documentation/macos-release-notes/macos-27_2-release-notes)
do not identify an APNs/virtualization fix. Apple's installer catalog and
distribution metadata confirm an **18,546,993,893-byte** full installer. The user
authorized this guest-only experiment. The native in-place upgrade completed;
the guest reports **27.2 / 26B5086k**, with retained VM/user identities and
user-approved MDM enrollment. The installer SHA-256 is
`7012b690a4394639255918bf76281fc17fed9747cfe584c11020fa1d6e192957`;
Apple signature verification and all five signed XAR member checksums passed.
The 27.0 guest's Software Update UI offered no beta channel while signed out of Apple
Account, and native `seedutil` reports that enrollment through that utility is
no longer supported.

After the incident review and Terminal comparison, the stopped fallback was
restored to its working 26 checkpoint, releasing the failed 27.0 installation's
changed disk blocks. Host free space increased to **47.78 GiB**. The checkpoint's
disk inode, modification time and size remained unchanged. Saved 27.0 diagnostics,
late command results and both original 27.0 guests remain retained. The fallback
was booted with an isolated read-only beta media share. A new native inventory
push acknowledged on restored 26.6.2 in **19.39 seconds**; logs again show BAA
certificates, a host-VM signed nonce and an APNs connection with no BAA failures.
The administrator was logged in, had a secure token and the volume had an owner.
The pinned download used validated ranges and ETag, with separate beta journals.
Embedded assets verified **27.2 / 26B5086k**. The host media reused the identical
downloaded package; its executable signatures, 886 resource files and 20 symlinks
matched the native Apple
package installation. The redundant guest copy was reclaimed and the small
installer program ran locally, with its media on the read-only host share.
Native installation started with about **29 GiB host** and **44 GiB guest** free,
without erase or reduced-security options.

**Result: the 27.2 guest upgrade did not restore reliable APNs on this 27.0 host.**

| Independent request / check | Result |
|---|---|
| First push after upgrade and confirmed login | No response within 45 seconds; elapsed 45.58 seconds. It acknowledged later, at 19:28:25 UTC, during the subsequent cold-boot/login check-in. |
| First push after full shutdown, cold boot and confirmed login | Acknowledged in 20.32 seconds; native response confirms 27.2 / 26B5086k, supervision and Apple silicon. |
| Next independent push in the same logged-in session | No response within 45 seconds; elapsed 45.72 seconds. |
| Final independent push after the session had settled | No response within 45 seconds; elapsed 45.77 seconds. |
| Transport controls | Verified guest-to-MDM TLS/readiness before and after every attempt; Apple courier TCP 5223 and activation TCP 443 pass. |
| Final current-boot native APNs capture | Twelve BAA acquisition failures; zero acquired BAA certificates, host-VM signed nonces or connected APNs interfaces. Existing `apsd-rk-scrt` key access still fails with `-25308`. |
| Lifecycle / assignments | Device and one user channel retain their normal enabled state and original 26 TokenUpdate; zero assigned declarations. |

The first beta boot logged transient connected-interface messages, so those counts
alone are not accepted as readiness. The cold-boot capture has none. The one timely
acknowledgement followed login and was adjacent to the older queued request's late
result. Its initiating event is not proved; the subsequent independent failures
mean it cannot establish reliable push delivery. No guest-side MDM polling or
manual enrollment enablement was used.

The [upstream discussion of automatic check-ins](macos27-vm-incidents.md#reboots-automatic-check-ins-and-apparent-recovery)
offers a plausible explanation for queued commands completing around login or
the following day while APNs remains broken. It does not establish the trigger
in this run. Reboot-associated queue progress and independent push delivery
must therefore be recorded separately.

The completed installer program was removed by macOS. Its closed host media was
reclaimed before the cold boot, which started with **21.82 GiB host free** and
about **39 GiB guest free**. The final two failures therefore do not depend on the
installation's low host headroom. The guest was shut down after final capture;
only its two pending expired requests were cleared. Completed/late results and
earlier cleared records were verified unchanged. Final host free space was
**21.97 GiB**. The 80 GB beta disk and working 26 checkpoint are preserved.

Private evidence includes `beta-outcome-summary.json`, `beta-upgrade-complete-*`,
`beta-upgrade-transport-health-*`, `beta27-*`, `failed27-late-results-*` and
tracked inventory directories ending in `192408376419Z`, `192806442393Z`,
`192923190115Z` and `193223715428Z`. LIVE-003 / LIVE-004 were not run on the beta
after readiness failed. No beta APNs recovery or new 27 feature acceptance is
claimed. The host remains **27.0 / 26A428**; a 27.2 host and a fresh beta IPSW
restore have not been tested. These results do not establish a precise Apple
implementation defect or a general claim about every 27.2 configuration.
