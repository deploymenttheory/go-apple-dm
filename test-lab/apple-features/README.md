# Apple OS 27 acceptance fixtures

These are protocol fixtures, not production policy. Automated tests validate each
fixture and exercise its delivery on every declared platform/version combination.
They do not prove that the OS applied a setting. See the
[coverage matrix](../../docs/operations/apple-os27-coverage.md) and
[device readiness procedure](../../docs/operations/mac-enrollment-testing.md#feature-acceptance-and-vm-readiness).

Some payloads are adapted from Apple's examples at the pinned
`third_party/apple-device-management/current/examples` revision. See [APPLE-LICENSE.txt](APPLE-LICENSE.txt).
The manifest retains source links, explicit target expectations and pending live
status. Example URLs, identifiers, credentials, hashes and dates must be replaced
in a private copy. Do not assign the whole directory to a real device.

Prepare only the selected feature and its dependencies:

```sh
python3 test-lab/apple-features/prepare.py --feature managedapp --out test-lab/local/apple27/managedapp
```

Add `--overrides PRIVATE.json` to replace complete Payload objects by fixture ID.
Dependencies are included automatically. This preparation is offline, refuses to
overwrite an output directory and creates no server assignments. Validate real
values through the normal declarations API before assigning. The returned
compatibility preview must show the selected declaration and all its dependencies
as eligible. A fixture is still pending when an issuer, endpoint, app or provider
is unavailable.

All bundles use the `com.deploymenttheory.acceptance.*` identifier namespace and
one activation. Run one bundle at a time on an isolated lab server; the identifiers
are intentionally stable so cleanup can be explicit. Upload only JSON files listed
in `bundle.json` (the manifest itself is not a declaration). Add each declaration
to a dedicated set, assign that set to the test device, then observe status.
For `app-privacy` and `website-privacy` on macOS, assign to the installing-user
channel instead; `macOSChannel` in the manifest records this requirement.
Mac app privacy identifiers must include the app's designated requirement as
`Bundle-ID {Designated-Requirement}`. Replace the generic bundle ID in the
private override before deployment.
Unassign the set and verify removal before starting the next bundle. Never remove
an unrelated assignment or a shared declaration.

`legacy-asset` and `managedapp` both use the example `data` asset, but require
different fetched content. In their separate runs, configure the former with a
profile plist and the latter with an app configuration property list using the
matching plist media type. Real HTTPS, certificates,
issuer challenges and signing identities belong under `test-lab/local`, which is
ignored by git. No private values belong in this fixture directory.

## Binary execution controls

The `binary-controls` fixture tests configured identifier matching together with
macOS's independent signing restriction. Use the
[execution contract and control procedure](../../docs/operations/application-identities.md#binary-execution-controls)
to classify results. An ad-hoc signed executable is not an eligible unrelated
control simply because its signature passes integrity verification.

For a deny-only case, use a disposable Developer ID signed target, an unrelated
eligible Developer ID app, an Apple system control and separate ad-hoc controls.
Establish that all run before assignment. While active, expect the matching target
and ad-hoc controls to be denied while the eligible unrelated controls run.
Withdraw the test configuration and confirm every baseline probe recovers. Keep
an independent native removal watchdog armed throughout the bounded assignment.
Use an explicitly designated native test device and preserve its enrollment.

Native observations cover the described deny-mode case on macOS 27.0 (26A428). Empty-list,
development-signed and unsigned cases, allow mode and managed-app exceptions
remain untested; the manifest's pending live status is not a blanket failure or
pass. Record a fresh result for the actual source revision and target when native
acceptance is required.

## Offline checks

Run the offline preparation checks with:

```sh
python3 -m unittest discover -s test-lab/apple-features -p '*_test.py'
```

## Assign and remove a selected bundle

Use an authenticated CLI context and replace example endpoints, hashes and
identifiers in private payload overrides. Review the target inventory and required
capabilities before assignment; compatibility is an explanation, not an approval
gate. Assignments schedule notifications immediately.

```sh
python3 test-lab/apple-features/prepare.py --feature FEATURE --out PRIVATE_DIR
# Repeat for each declaration named in bundle.json:
dmctl declarations put -file PRIVATE_DIR/DECLARATION.json
dmctl sets add LAB_SET DECLARATION_IDENTIFIER
dmctl sets assign device DEVICE_ID LAB_SET
dmctl enrollments compatibility device DEVICE_ID
dmctl notify
```

For a supported installing-user payload, use `user USER_ID` and `-parent DEVICE_ID`
in assignment, compatibility and status commands. Observe both protocol status and
actual feature behavior using [status inspection](../../docs/operations/status-and-profile-inspection.md).
Record the source revision, OS/build, expected and observed outcome, timestamps,
limitations and cleanup privately. Missing dependencies are blocked acceptance,
not successful tests.

```sh
dmctl sets unassign device DEVICE_ID LAB_SET
dmctl notify
```

For a user-channel case, unassign `user USER_ID LAB_SET -parent DEVICE_ID`.
Verify removal before deleting test set members/declarations. Restore any separate
service state or user choices affected by the test and preserve unrelated policy.
Software-update fixtures with placeholder dates are validation examples; use real
metadata and device-local deadlines only for a separately selected installation test.
