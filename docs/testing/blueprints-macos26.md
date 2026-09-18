# Blueprint acceptance on macOS 26

The maintained live scenarios are implemented in
[`server/internal/bench/live_blueprints.go`](../../server/internal/bench/live_blueprints.go).
They use the ordinary Blueprint and configuration profile administration routes,
real APNs wakes, native DDM status reports and acknowledged MDM `ProfileList`
commands. They require a designated macOS 26 test device or VM with completed MDM
enrollment. LIVE-006 additionally requires its installing user to be logged in and
MDM-enabled. These scenarios are unsupported in simulated mode.

## Assertions

Each scenario uses a unique Blueprint identifier, profile identifier and UUID.
The profile contains a managed preference in a unique
`com.deploymenttheory.acceptance.*` domain. It does not configure an existing
application's preferences. Its `PayloadScope` is `System` for LIVE-005 and `User`
for LIVE-006.

| Step | Required evidence |
|---|---|
| Inventory | A new `DeviceInformation` command is accepted through APNs and acknowledged by the selected device; its OS version is 26.x and its build is nonempty. |
| Composition | A Blueprint contains a native management status subscription and a `LegacyProfile` configuration using the uploaded profile's `ProfileURL`, with separate `ActivationSimple` declarations. |
| False predicate | The native subscription and unconditional activation are valid and active. The conditional activation and profile configuration are inactive. A fresh `ProfileList` omits the profile. |
| Identical publication | Repeating the same source with the current revision preserves the returned source revision and compiled declarations. |
| True predicate | Updating `Predicate` to `TRUEPREDICATE` produces fresh valid, active status. A fresh `ProfileList` contains the original `PayloadIdentifier`, `PayloadUUID` and expected display name, with `Source` equal to `Declarative Device Management`. |
| Profile replacement | Uploading revised bytes and publishing their revision changes the profile configuration's reported `ServerToken`. The same profile identifier and UUID appear exactly once, with the revised display name. |
| Clear and republish | Publishing an identifier-only source removes the temporary declarations from native status and removes the profile from `ProfileList`. Republishing the complete source installs it again without assigning the Blueprint again. |
| Unassign and reassign | Unassignment removes the declarations and installed profile. Reassignment restores them, with fresh native status. |
| Delete while assigned | Deleting the Blueprint removes its declarations from native status and its profile from a fresh `ProfileList`. |
| macOS 27 field on macOS 26 | LIVE-005 publishes `ProfileAssetReference`. Compatibility reports the configuration as withheld for `unsupported-target`; a compatible subscription and activation in the same publication receive fresh device status, while the profile configuration is absent and the profile remains uninstalled. |

The inactive profile configuration may report validity `unknown` before its
activation becomes true. Active declarations must report `valid`. Changed
declarations must have a report timestamp at or after the corresponding mutation;
unchanged declarations may retain earlier status because Apple reports changes
incrementally. A server-side publication response alone cannot satisfy the native
installation or removal checks.

Deferred cleanup runs with its own deadline even if the scenario fails or its
context expires. Cleanup errors fail the scenario. Uploaded immutable profile
revisions and retained declaration versions remain available to administrators;
the temporary Blueprint, its assignments and its installed profile are removed.

## Run

Build the working tree and use an explicitly live bench workspace containing the
server's CA certificate and admin credential. The server must already be running
with DDM status subscriptions enabled and an HTTPS public URL reachable by the VM.

```sh
make bench-build
test-lab/local/bin/dmctl bench run \
  -workspace /path/to/live-workspace \
  -attach-url https://your-mdm-server.example \
  -scenario blueprints \
  -device-id DEVICE_ID \
  -user-id INSTALLING_USERS_GENERATED_UID \
  -revision SOURCE_REVISION \
  -report-dir /path/to/private/results
```

Use `-scenario LIVE-005` or `-scenario LIVE-006` to run one channel. The user ID is
the local account's `GeneratedUID`; the scenario builds the complete user enrollment
identity with its parent device ID. The user assignment and status requests include
that parent explicitly.

The report directory contains `results.json` and `junit.xml`. Phase evidence lives
under the workspace's `evidence/blueprints-live-*` directories: inventory,
publication records, configuration profile upload metadata, declaration status,
decoded `ProfileList` responses and the compatibility result. Record the running
server and CLI binary hashes, source revision and working-tree file hashes alongside
these files. All evidence directories are private and remain outside source control.

## Recorded VM run

On 18 September 2026, the device and user lifecycle scenarios passed on a
Guestweave VM running macOS **26.6.2 (25G83)**. The reference server used its combined
role and SQLite. The test VM was cloned from the preserved `macos26-apns-working`
checkpoint; the server used a separate database copy with the current DDM
`0001_init.sql` applied to newly created DDM tables. Existing enrollment identities
were retained, and only the test VM and its user channels were enabled in that copy.

The final run is retained locally in
`test-lab/local/blueprints/run-20260918T101643Z/`, including `results.json`,
`junit.xml` and `manifest.json`. The manifest records the VM version, source file
hashes, binary hashes and the two phase-evidence directories. Accepted native status
is saved separately from cleanup status so removal does not overwrite installation
or compatibility evidence.

| Scenario | Result | Duration |
|---|---|---|
| LIVE-005 — device channel, including macOS 27 field exclusion | Passed | 101.016 seconds |
| LIVE-006 — MDM-enabled user channel | Passed | 82.257 seconds |

Source: `66218458937e0763100af0d4ccf54afe91e463ae` plus the uncommitted working tree.
The manifest's production source digest is
`c34a129fa5615a05a13e10bce974fa00f7350c04b13d04e0b6167084f999fe2e`.

| Executable | SHA-256 |
|---|---|
| dmserver | `50f20a7c4ee1057f4f10ccc6a3549a171c9a69dd2d41538967baa8d322c7dc71` |
| dmctl | `551063d9bf4074ba16ae9554b5110ff759a655b4cd8d748070e7962757b61e9f` |

The bench and CLI unit suites passed. Race-enabled regression tests additionally
verified rejection of stale or invalid native status, incorrect profile identity or
source, duplicate and missing profiles, unchanged replacement tokens, malformed
responses, failed requests and unavailable evidence storage. The CLI and simulated
acceptance adapters also passed. Merging those coverage profiles measured **95.4%**
for `server/internal/bench` and **96.8%** for `live_blueprints.go`; lint reported no
issues. These fixture-based checks validate the test runner's assertions and
cleanup. Native acceptance is established by the separate VM run above.

This establishes the tested native DDM lifecycle and unsigned configuration profile
delivery through `ProfileURL` on both channels. `ProfileList` proves installation
and replacement; this suite does not read the custom managed preference through
an application. Signed profile delivery, native asset-reference delivery on macOS
27, split server topology and other SQL backends require separate evidence.

Apple's field names and availability are defined in the pinned
[`LegacyProfile` schema](../../third_party/device-management/declarative/declarations/configurations/legacy.yaml)
and [`ProfileList` schema](../../third_party/device-management/mdm/commands/profile.list.yaml).
Positive acceptance for `ProfileAssetReference` requires macOS 27; its exclusion on
macOS 26 is a compatibility assertion.
