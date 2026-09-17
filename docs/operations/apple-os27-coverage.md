# Apple OS 27 feature coverage and mixed fleets

Reviewed 17 September 2026. Scope: the Go library and reference server. Generated
API availability, server delivery eligibility, and acceptance by a physical device
are separate checks. The first physical macOS 27 pass retained **failures and
remaining acceptance**; follow-up testing is in progress. See the
[live validation record](../testing/macos27-live-validation.md) for completed cases
and remaining behavior checks. The [handoff](../testing/macos27-handoff.md) describes
the maintained procedure.

The subsequent 40 GB macOS 27 Guestweave VM reached user-approved SCEP profile
installation, but its APNs identity key generation fails and no TokenUpdate
arrives. Guest command/DDM acceptance is blocked before feature delivery; see
[the recorded blocker](../testing/macos27-live-validation.md#guest-continuation--17-september-2026).
The complete automated matrix, thirteen required contracts and unchanged 95%
coverage gate passed at `eb5208a`. Neither that result nor profile installation
substitutes for the remaining native behavior checks.

Sources: [enterprise changes](https://support.apple.com/en-us/148830),
[update management](https://support.apple.com/en-gb/guide/deployment/depd30715cbb/web),
[general release notes](https://support.apple.com/en-us/127257), and the pinned
[Apple schema](../../third_party/device-management). Wire names and platform
constraints below come from that schema, including its historical input. The
primary pin remains `b0180185a5e4077070710033341b71d0cbe1a18a`; the historical pin
remains `67045e2fa06f528b196c01edee6a8bf88b844beb`. No schema pin was advanced.

## Delivery contract

The reference server resolves OS, version, channel and enrollment capabilities
from stored inventory. Known commands and the contents of `InstallProfile` are
checked at enqueue and again at dispatch. Generated command validation checks the
command envelope and fields; `validateInstallProfileContents` inspects the embedded
configuration profile, its payloads and availability. Signed bytes are preserved.
The public `profile/inspect` package also powers `dmctl profiles lint`.

DDM resolves the target before constructing each manifest and checks previously
advertised declarations before serving them. Unsupported configurations are
withheld; missing, incompatible or wrongly typed asset references withhold their
dependents. Activations and status subscriptions are pruned and receive matching
content tokens. Shared supported settings remain available. A failed inventory
lookup returns an error without replacing the last snapshot. Unknown OS/version
permits only baseline inventory subscriptions and their activation.

Library consumers opt into DDM filtering with `ddm.Config.EnrollmentTarget`.
Leaving it nil preserves existing library behavior. Command consumers retain the
explicit `ValidateTargets: false` escape hatch. `devicemanagement/osversion`
provides the shared version type, parsing/comparison and named macOS major
constants. Generated support tables retain precise introduction, deprecation
and removal versions for each platform. Target versions and support boundaries
use `osversion.Version` directly. Consumers of the removed support version API
must update imports and calls as described in the
[migration guide](os-versions.md#migrating-from-the-support-version-api).

Reviewed SSO enum floors are generated as value availability metadata, queryable
through `profiles.ValueSupport(path, value)`, and enforced by typed `Validate`
methods. The same methods enforce device-local deadlines, automatic-download/
install relationships, content-cache trust requirements and app identity/binary
rules. Callers no longer need a separate compatibility-validation wrapper.

`GET /enrollments/{channel}/{id}/compatibility` and
`dmctl enrollments compatibility device DEVICE-ID` preview eligibility without
persisting snapshots, changing tokens or sending notifications. Reasons contain
identifiers and fixed codes, not payload values. Unknown/custom profile payloads
and encrypted contents remain explicitly **unvalidated** in inspection reports;
the server does not claim it can decrypt them. External legacy-profile URLs are
not fetched by validation. Device-advertised status capabilities select automatic
subscriptions; declaration filtering uses schema availability and observed target
capabilities, not an inference that every advertised app/provider is installed.

## Enterprise feature matrix

`F` means the fixture is tested through both parsing and DDM delivery on every
schema OS family at 26.0, 26.4, 26.6.2 and 27.0, with explicit allowed and rejected
combinations in [manifest.json](../../test-lab/apple-features/manifest.json).
iPadOS uses the iOS schema family. Fixture availability applies to the exact
fields in that fixture, not every variant of its declaration type.

The reviewed [macOS 27 inventory](../../test-lab/apple-features/macos27-coverage.json)
names all 32 fixture IDs and 50 source cases containing 73 explicit macOS 27
introduction, deprecation or removal boundaries at the pinned schema. The required
`TestSeedOS27CoverageInventory` contract rejects missing or changed cases and
checks 663 affected inherited paths against compiled metadata at all four versions
in six enrollment contexts. It separately checks the twelve reviewed SSO enum
floors described in prose. Source cases excluded from Mac acceptance have an
explicit platform reason. The inventory records native test plans, not passes;
the live report remains the evidence record. No fixture-count threshold substitutes
for named case coverage.

| Requirement | Implementation / automated evidence | Physical acceptance after upgrade |
|---|---|---|
| App and binary execution policy | `AppSettings`; F `binary-controls` | Allow/deny only a disposable signed fixture; verify managed-app exception and removal. |
| App consent | App privacy declarations; F `app-privacy` | Record consolidated prompt, allowed services and revocation. |
| Website consent | Website privacy declarations; F `website-privacy` | Use controlled HTTPS origin; verify origin scope and prompt. |
| Accessibility Live Recognition | `AccessibilitySettings.Vision.AllowLiveRecognition`; F `accessibility` | Verify Ask about Images and Surroundings behavior on eligible hardware, then removal. |
| Web content filter plug-in | F `webcontent-filter`; generated provider, socket, packet and URL filter settings | Reviewed signed provider and endpoint required; verify actual filtering and removal in the guest. |
| OpenID authentication | Platform SSO declaration; F `platform-sso`; legacy-profile OpenID rejection on 26 | Provider extension and test identity required; exercise FileVault, login and unlock separately. |
| Other SSO controls | F `platform-sso`, `extensible-sso`; `WebAuthentication`; legacy `WebLoginURLAllowList` and `AllowWebLoginPasswordSync`; reviewed OpenID/Touch ID/Watch enum floors | Record web allow-list/password-sync, network/captive flow, biometric/watch policy and guest FileVault behavior separately. |
| Enhanced AppleCare diagnostics | `TriggerEnhancedLogCollection`, `CancelEnhancedLogCollection`; command and status contracts | Valid AppleCare token; supervised Mac **user channel**. Trigger, observe status, cancel, observe completion/error. iOS/iPadOS/tvOS device channels need separate hardware. |
| Declarative networking | F seven `network.*` fixtures; typed asset dependency checks | Real DNS/VPN/relay endpoints and extensions; measure connectivity and restore it. Always-on fixture is iOS/visionOS, not macOS. |
| Cache reporting | F `content-cache`; native report codec/receiver; persistent admin API described below | Mac must send its native report; record device-bound persistence, paging and credential rotation. |
| ManagedApp | F `managedapp` with data, password, certificate and identity assets and extension configuration; F `managedapp-legacy-config`; `app.managed.list` config-state | Signed app implementing ManagedApp required; verify each consumer, asset rotation and fresh configuration status. |
| Siri AI restrictions | `SiriSettings.AllowSiriAI`; F `siri`; separate `ForceReduceSensitiveContent` fixture `siri-sensitive-content` | Verify each policy's behavior and removal on eligible hardware/account/region. Preference application alone does not prove behavior. |
| Visual Intelligence / Calendar editing | `IntelligenceSettings`; F `intelligence` | Verify each configured restriction separately. |
| Package cleanup | `Package.UninstallBehavior`; F `package-removal`; `package.list` status | Install disposable signed package; remove declaration and verify only its test files are removed. |
| Assessment framework | App implementation boundary; [Apple assessment session](https://developer.apple.com/videos/play/wwdc2026/230/) | Requires an assessment app and entitlement. No invented MDM command. |
| ACME/SCEP accessibility | F `acme`, `scep`; generated `Accessible` fields retained on supported 26 targets | Real issuer, locked/unlocked availability and renewal. Distinguish modern asset form from older plural `credentials.*` forms. |
| Enrollment, lockdown, setup and push status | Typed `mdm.enrollment-type`, `security.lockdown-mode`, `mdm.is-awaiting-configuration`, `mdm.push-magic` and `mdm.push-token`; generic status persistence and subscription filtering | Capture fresh values and compare actual state; absence is not false. Retain push credentials privately. |
| Profile assets | F `legacy-asset` and `legacy-interactive-asset`; older `legacy-url` remains valid on 26; legacy compatibility contract | Preserve noninteractive apply/remove results; separately observe user presentation, acceptance/decline, update and removal for interactive profiles. |
| Liquid Glass setup | DEP `SkipSetupItems` validation uses generated setup enums | ADE test at Setup Assistant; cannot prove by upgrading an already configured Mac. |
| ADE reliability | Existing ADE/enrollment and retry flows | Separate erased/spare ADE Mac; preserve enrollment and retry evidence. |
| Accessibility permission behavior | Existing PPPC schema retained, inspector reports deprecation | User notification and ability to revoke require UI observation; do not read the TCC database. |
| Lock-screen network controls | `LoginWindow.ForceWifiConfigurationOnLockScreen` and `ForceCaptivePortalConnectionFromLockScreen`; source and inherited-boundary contract | Verify Wi-Fi and captive-portal UI separately on a suitable test Mac. |
| Legacy update retirement | Command/query/profile availability plus dispatch recheck; software-update and mixed-fleet contracts | Retained 26 device accepts supported legacy management; 27 uses DDM. No legacy commands dispatched to 27. |
| Menu-bar behavior, App Attest, privacy CLI/database changes, Rosetta, SMB and shared-login fixes | OS/app behavior; no new server API implied | Optional host/app smoke checks. Rosetta may require operator installation; no reinstall action is part of this prep. |

The source inventory also explicitly retains deprecated content-cache, DNS proxy,
DNS settings, relay, password-policy and application-access profiles. PPPC Camera,
Microphone, Accessibility, SpeechRecognition and BluetoothAlways deprecations are
checked separately from removals. macOS 27 removes the legacy SoftwareUpdate
profile, update commands and query values, plus the application-access deferral
and Rapid Security Response fields recorded in the inventory. LiquidGlass is a
new setup skip key; OSShowcase is removed. Deprecation must not withhold a payload
as though Apple had removed it.

Fresh `content-cache.info`, `content-cache.parents`, `content-cache.peers` and
`content-cache.status` observations are distinct from the native report receiver.
Enhanced diagnostics similarly requires the AppleCare token, status and timestamp
items to be observed separately from successful command encoding.

## Software update coverage

The schema and [software-update helpers](../../scripts/softwareupdates) are the
protocol integration points. Selecting an available release and scheduling real
installation remains an operator policy decision. Tests never install an OS.

| Concern | Code / fixture evidence | Live proof required |
|---|---|---|
| ADE minimum OS | `enroll/ade.Policy.MinimumOS`, signed MachineInfo and update response tests | Separate enrollment on a device below the chosen minimum. |
| Deferrals | `SoftwareUpdateSettings.Deferrals`; F `update-macos`, `update-ios`; generated 1–90 bounds | Verify displayed availability against actual release date. |
| Cadence | DDM `RecommendedCadence`; F `update-ios`; legacy query removal tests | iOS/iPadOS device; not a macOS cadence assertion. |
| Automatic actions | Download/install/security enums and relationship validation | Test each policy with prerequisites satisfied. |
| Background improvements | DDM `RapidSecurityResponse`; retained historical wire name | Observe policy, removal and enforcement on an offered build. |
| Administrator authorization | `AllowStandardUserOSUpdates`; F `update-macos` | Standard and administrator accounts. |
| Deadline/version/build/details | F `update-deadline`; strict local-time syntax checks | Pick real available target and future local deadline; observe status and UI. |
| Notifications | `Notifications` in F update fixtures | Record normal and reduced notification modes. |
| Bootstrap authorization | Existing bootstrap-token check-in/storage flows | Apple silicon authorization; retain keys privately. |
| Progress, errors, retries/offline | Typed `softwareupdate.*` status, status list/error storage | Observe download/preparation/install, interruption, recovery and final inventory. |
| Shared iPad | Target/shared-mode checks; existing user/device channel support | Separate Shared iPad; sign users out and record storage prerequisites. |

## General release-note disposition

The general article is not a list of new MDM endpoints. Siri and Visual
Intelligence map to the restrictions above. Photo editing, Safari features,
natural-language Shortcuts, Liquid Glass appearance, family controls and search,
AirDrop and responsiveness improvements are OS/app behavior. Existing schema
restrictions continue to apply where modeled; this project does not implement
those user interfaces or promise that a family-control feature has an enterprise
management equivalent. These items are accounted for without adding unsupported
server APIs. Hardware, account and regional availability require device checks.

## Content-cache operations

Reporting is opt-in. Configure `DM_CONTENT_CACHE_URL=https://mdm.example.test`
(or `-content-cache-url`) on serving roles and admin roles sharing the same state.
Only an HTTPS origin is accepted. `DM_CONTENT_CACHE_RETENTION=720h` (or
`-content-cache-retention`) overrides the default 30 days. Negative or invalid
retention is rejected; the zero configuration value selects the default.

Using the existing authenticated `dmctl` context:

```sh
dmctl content-cache rotate DEVICE-ID
dmctl content-cache reports DEVICE-ID
dmctl content-cache revoke DEVICE-ID
```

`rotate` returns a secret URL once. Store it privately and put it into the device's
`ContentCaching.ManagementStatusTarget`. It is a per-device ingestion credential,
never an admin token. Rotating immediately invalidates the previous URL. Deploy
the replacement URL to the device; ingestion at the old URL fails until it does.
The matching admin API uses POST/DELETE
`/enrollments/device/{id}/content-cache/credential` and GET
`/enrollments/device/{id}/content-cache/reports`. Credential operations require
`manageContentCacheCredentials`; report reads require `readEnrollmentStatus`.
They inherit enrollment resource scoping and the existing mutation/audit boundary.

The receiver accepts bounded native JSON reports using POST or PUT. HTTPS is
required, including explicit trusted-proxy configuration for TLS termination.
Its URL is redacted before internal middleware; the reverse proxy must redact
`/content-cache/metrics/*` independently. Payload host identifiers are untrusted;
authentication supplies the stored enrollment. Only credential hashes are stored.
Report writes recheck credentials in the transaction shared with rotation and
revocation. Reports use enrollment-bound cursors and expire by store time.

Memory, SQLite, PostgreSQL and MySQL use the same transactional `state.Store`
implementation and contract suite. Existing SQL state migrations, encryption,
backup and expiry maintenance apply; no separate cache-report migration is needed.
Memory is deliberately ephemeral. This endpoint has no bearing on delivery to
macOS 26: the reporting declaration is withheld there while existing management
continues.
