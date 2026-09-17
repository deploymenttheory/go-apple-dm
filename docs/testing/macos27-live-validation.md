# Physical macOS 27 validation

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
| App and website consent | App grants and temporary Safari grants confirmed. Combined-prompt behavior, UI revocation and website origin isolation remain unconfirmed; app permission reset was performed separately as cleanup |
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
standalone server publication dependency remains as documented in
[release sequencing](macos27-prep-validation.md#release-sequencing).
