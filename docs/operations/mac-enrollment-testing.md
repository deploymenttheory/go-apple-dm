# Manual Mac enrollment with ACME and SCEP

This procedure tests manual enrollment on an Apple silicon Mac using the reference
server. Run ACME and SCEP separately, removing the enrollment between runs. Manual
installation requires the Mac user's action in System Settings. The replacement
command later requests an MDM profile installation; run it only after the
operator approves that test. No test in this runbook erases the Mac.

## Prepare

Use a live workspace, a fixed loopback listener and a valid MDM APNs certificate
with its matching private key. The ordinary app-push certificate for
`com.weaveplatform.deviceweave` cannot authenticate MDM pushes. See the existing
[bench runbook](../../test-lab/README.md) for certificate acquisition/import.

Create a private admission policy file naming the Mac's hardware UUID, then set
`Settings.DM_ENROLLMENT_POLICY_FILE` in the workspace's `bench.json` to its
absolute path before starting the server. For a manual profile requested by UUID,
use `{"devices":[{"udid":"<hardware UUID>"}]}`. The simulated bench supplies its
own restricted fixture policy; a live workspace requires operator configuration.
See [enrollment security](enrollment-security.md) for device/account rules.

```sh
make bench-enrollment-preflight BENCH_IDENTITY=acme
make bench-trust BENCH_TRUST_FILE=test-lab/local/trust.mobileconfig
make bench-up
```

Preflight reports unavailable credentials as blocked. A successful local preflight
does not prove Apple connectivity or device installation. Trust the exported CA
through macOS before fetching profiles from the private HTTPS endpoint. Native
TLS, the identity issuer and public service discovery use the workspace's configured
certificates; `DM_ENROLL_TLS_ANCHOR_FILE` controls the HTTPS anchors in the server.

Keep `DM_ALLOW_REENROLL` disabled outside the explicit re-enrollment handoff below.
Live ACME uses Apple's attestation anchors and
requires hardware-bound, attested EC identities. Simulator anchors and unattested
fallback are for explicitly configured fixtures, not live acceptance.

## First enrollment

Use the Mac's hardware UUID as `BENCH_DEVICE_ID` (`ioreg -rd1 -c IOPlatformExpertDevice`
shows `IOPlatformUUID`). Obtain the installing user's GeneratedUID with
`dscl . -read /Users/<local-account> GeneratedUID`; use that exact value as
`BENCH_USER_ID`. In another terminal:

```sh
make bench-run BENCH_SCENARIO=E2E-027
make bench-profile BENCH_DEVICE_ID='<hardware UUID>' BENCH_IDENTITY=acme \
  BENCH_PROFILE_FILE=test-lab/local/acme.mobileconfig
```

Review and install that profile in System Settings. Its rights permit inventory,
profile inspection and profile installation/removal (mask 19). It contains the
required trust material, one ACME identity and one MDM payload. The server records
actual certificate issuance separately from profile generation.

After installation, run:

```sh
make bench-run BENCH_SCENARIO=LIVE-002 BENCH_DEVICE_ID='<hardware UUID>' \
  BENCH_USER_ID='<installing user GeneratedUID>'
```

This requires a pinned ACME-issued identity, Authenticate, TokenUpdate, an accepted
APNs wake, an acknowledged DeviceInformation response containing OSVersion and
BuildVersion, and an independently acknowledged ProfileList on the exact
installing user's management channel. Other local
users are not automatically considered managed. Results remain blocked until
the required device evidence exists.

## Replacement

```sh
make bench-replace BENCH_DEVICE_ID='<hardware UUID>' BENCH_IDENTITY=acme
test-lab/local/bin/dmctl api GET \
  '/enrollments/device/<hardware UUID>/replacement'
```

The `api` command needs the normal server URL, admin credential and HTTPS trust
configuration described in the [reference-server guide](reference-bench.md).
The response reports the attempt ID, state, delivery, acknowledgment and certificate
fingerprints without exposing the profile or issuance credentials. A push failure
does not cancel the attempt: inspect it and retry the normal enrollment push route.
An operator can cancel with `DELETE /enrollments/device/{id}/replacement/{attempt}`.

Expect `committed` only after both successful command acknowledgment and the new
identity's TokenUpdate. Verify the profile remains installed on macOS, then rerun
LIVE-002. Failed, cancelled and expired attempts preserve the old enrollment.

For live rollback evidence, record an actual macOS-reported installation failure,
inspect the previously installed profile/identity on the Mac and repeat the inventory
test using the old identity. A server cancellation by itself is not proof that macOS
attempted and rolled back an installation. The automated failed-replacement scenarios
use simulator behavior; they must not be reported as a real-device rollback pass.

## Repeat with SCEP

Prepare a fresh SCEP profile and a private state/configuration backup before the
handoff. Keep the admission policy restricted to the test Mac. Temporarily enable
`DM_ALLOW_REENROLL` and restart the server with the same database, URL and identities;
this permits a new certificate to restart the removed enrollment. It does not
permit the old certificate to reactivate it.

The operator then removes the enrollment through System Settings, confirms that
the Mac is no longer managed, and installs the fresh SCEP file. Enrollment grants
expire after their configured lifetime (one hour by default). Generate a new
profile after a failed or expired attempt instead of reusing a consumed credential.

After the device and returning user's fresh TokenUpdate arrive, disable
`DM_ALLOW_REENROLL` again and restart the server before running LIVE-003 with both
`BENCH_DEVICE_ID` and `BENCH_USER_ID`. The returning user must receive fresh tokens;
old queued commands and push credentials must not survive the removed enrollment.
If installation fails, preserve the server/device evidence and generate a new ACME
profile for recovery using the same controlled handoff.

Repeat replacement with `BENCH_IDENTITY=scep`. Record separate result directories
using `dmctl bench run -report-dir ...` so the two identity runs remain distinguishable.

ADE Setup Assistant activation requires a separate test involving an assigned device
and Apple Business Manager/School Manager. Manual enrollment does not prove that
path. Apps and Books user invitations and licensing concern app distribution;
they are not prerequisites for manual enrollment and inventory.

## Feature acceptance and VM readiness

Before assigning a feature, run fresh tracked `DeviceInformation` queries for
`OSVersion`, `BuildVersion`, `IsSupervised` and `IsAppleSilicon`, and inspect the
server's compatibility target. `LIVE-001` supplies OS/build evidence; query the
capabilities separately. When permitted, `SecurityInfo.ManagementStatus` supplies
ADE and user-approved observations. Do not infer them from a profile an operator
uploaded. Select a user channel by its canonical identifier and parent device.

Use the [feature fixture workflow](../../test-lab/apple-features/README.md) one
bundle at a time. Compare compatibility preview, fetched declarations and native
status, then test the intended OS behavior separately. Unassign only the test
set, verify status removal and restore its observable effects. Removing a
configuration does not necessarily undo user consent or reactivate/deactivate a
service. [Blueprint acceptance](../testing/bench.md#blueprint-acceptance) and
[application identity checks](application-identities.md#native-artifact-verification)
provide focused procedures.

For a VM, successful HTTPS and profile installation do not establish APNs readiness.
Require a new independently pushed inventory request, its matching command UUID
and a bounded acknowledgment after enrollment, after restart and during an idle
logged-in session. Preserve a working checkpoint before destructive OS changes.
Readiness failures stop downstream feature acceptance.

Tests on macOS 27.0 (26A428) and a 27.2 beta guest (26B5086k), hosted on 27.0,
observed APNs/BAA key failures despite working HTTPS; a 26.6.2 guest control on the
same host delivered commands. These observations are limited to those tested
configurations. They do not identify a universal virtualization defect or prove
that another host/guest release has the same behavior. Late responses around boot
or login show queue progress, not that an independent APNs notification caused it.
Apple's [command delivery protocol](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device)
separates the wake notification from the subsequent HTTPS exchange.

Feature prerequisites include appropriate enrollment access rights, provider
extensions and endpoints, disposable signed apps, and real issuer/IdP credentials.
Enhanced diagnostics needs an AppleCare token and the supported channel. Software
update enforcement and ADE Setup Assistant require separate, explicitly selected
targets. A Mac run cannot establish iOS/iPadOS, Shared iPad, tvOS, visionOS or watchOS
acceptance. Native results should state which behavior was measured, rather than
classifying an entire schema family as passed.
