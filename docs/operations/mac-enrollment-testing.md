# Manual Mac enrollment with ACME and SCEP

The first device milestone uses an Apple silicon Mac and the maintained reference
server. Run ACME and SCEP separately, removing the enrollment between runs. Manual
installation requires the Mac user's action in System Settings. No command below
installs profiles, changes system trust or erases the Mac automatically.

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

Keep `DM_ALLOW_REENROLL` disabled. Live ACME uses Apple's attestation anchors and
requires hardware-bound, attested EC identities. Simulator anchors and unattested
fallback are for explicitly configured fixtures, not live acceptance.

## First enrollment

Use the Mac's hardware UUID as `BENCH_DEVICE_ID` (`ioreg -rd1 -c IOPlatformExpertDevice`
shows `IOPlatformUUID`). In another terminal:

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
make bench-run BENCH_SCENARIO=LIVE-002 BENCH_DEVICE_ID='<hardware UUID>'
```

This requires a pinned ACME-issued identity, Authenticate, TokenUpdate, an accepted
APNs wake, an acknowledged DeviceInformation response containing OSVersion and
BuildVersion, and the installing user's enabled management channel. Other local
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

Remove the enrollment through System Settings and confirm the Mac is no longer
managed. Export a new file with `BENCH_IDENTITY=scep`, install it, and run LIVE-003.
Repeat replacement with `BENCH_IDENTITY=scep`. Record separate result directories
using `dmctl bench run -report-dir ...` so the two identity runs remain distinguishable.

ADE Setup Assistant activation is a subsequent test involving an assigned device
and Apple Business Manager/School Manager. Manual enrollment does not prove that
path. VPP user invitations and license assignment concern later app distribution;
they are not prerequisites for this enrollment and inventory milestone.
