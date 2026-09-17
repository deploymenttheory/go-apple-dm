# Apple OS 27 acceptance fixtures

These are protocol fixtures, not production policy. Automated tests validate each
fixture and exercise its delivery on every declared platform/version combination.
They do not prove that the OS applied a setting. See the
[coverage matrix](../../docs/operations/apple-os27-coverage.md) and
[reboot handoff](../../docs/testing/macos27-handoff.md).

Some payloads are adapted from Apple's examples at the pinned
`third_party/device-management/examples` revision. See [APPLE-LICENSE.txt](APPLE-LICENSE.txt).
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

Run the offline preparation checks with:

```sh
python3 -m unittest discover -s test-lab/apple-features -p '*_test.py'
```
