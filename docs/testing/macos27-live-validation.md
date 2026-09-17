# macOS 27 live validation

**Current guest continuation:** the 40 GB Guestweave VM has a user-approved
SCEP profile, but MDM command/DDM acceptance is blocked by guest APNs identity
key generation. No TokenUpdate or installing-user channel has arrived. See
[the guest result](#guest-continuation--17-september-2026). The conclusive physical
results below remain retained.

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
The guest remains visible and enrolled for diagnosis; no feature policy is
assigned. Guest-dependent binary, package/app and permission variants remain
blocked at transport readiness. Retained physical-device passes and failures
are unchanged.
