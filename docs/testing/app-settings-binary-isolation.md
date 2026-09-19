# App Settings binary isolation on macOS

On the tested physical Mac running **macOS 27.0 (26A428)**, a deny rule for one
application also prevented unrelated ad-hoc signed executables from starting.
Unrelated Developer ID and Apple signed controls continued to run. Removing the
declaration restored all controls. The failure occurs in native enforcement after
the reference server delivers the intended deny-only payload.

This investigation narrows the [application identity acceptance failure](application-identities-macos.md#physical-host-app-settings-enforcement).
It does not establish isolated enforcement on other macOS builds or a successful
DDM application installation.

## Protocol expectation

Apple's [App Settings documentation](https://developer.apple.com/documentation/devicemanagement/appsettings)
permits a `DeniedBinaries` entry containing only `SigningID`.
The [allowed settings dictionary](https://developer.apple.com/documentation/devicemanagement/appsettingsallowedobject)
describes denials by matching binary identifiers, with all supplied identifiers
required to match. An allow list restricts execution when `AllowedBinaries` is
present. The [binary identifier dictionary](https://developer.apple.com/documentation/devicemanagement/appsettingsallowed_binaryidentifierobject)
defines `SigningState` as a matching qualifier with default `All`.

The expected isolation result is that a matching executable cannot start while
unrelated executables retain their baseline behavior. The reviewed documentation
does not describe activation of a deny-only rule as globally rejecting ad-hoc
signatures. Whether the observed extra restriction is intentional requires Apple
confirmation; a valid declaration status does not answer that question.

## Minimal reproduction

The [configuration fixture](fixtures/app-settings-signing-id-deny.json) contains
the exact tested payload, with a reusable example declaration identifier. It has
one SigningID and no CDHash, TeamID, PathPrefix, SigningState or AllowedBinaries.
Activate it with:

```json
{
  "Type": "com.apple.activation.simple",
  "Identifier": "com.example.binary-isolation.activation",
  "Payload": {
    "StandardConfigurations": ["com.example.binary-isolation.settings"]
  }
}
```

Use an explicitly designated supervised physical test Mac. This reproduction
temporarily prevents Homebrew tools from launching on the affected build.

1. Record the OS/build, selected enrollment's hardware identity, active App
   Settings declarations and native launch policy. Start with no other binary
   restrictions. Retain the existing enrollment and server database.
2. Select an installed Developer ID signed target and confirm its signing ID
   with `codesign -d --verbose=4`. The recorded target was Suspicious Package 4.5,
   with signing ID `com.mothersruin.SuspiciousPackageApp`. Check signatures with
   `codesign --verify --strict --all-architectures`.
3. Establish baseline execution for the target, an unrelated Developer ID app,
   an Apple executable and unrelated ad-hoc signed executables. Record their
   signing metadata independently. Use commands that exit without modifying data;
   this run used VLC `--version`, `/usr/bin/true`, Python printing a marker and
   Git `--version`. Preserve any pre-existing application instance.
4. Before assignment, arm an independent native removal watchdog. The recorded
   watchdog used `/bin/sh`, `/bin/sleep` and `/usr/bin/curl`, waited 60 seconds,
   then retried removal of only its own enrollment/set assignment and APNs
   notification six times. Verify its authenticated removal transport before
   assignment. Store credentials privately, outside command-line arguments.
5. Publish the configuration and activation, assign only that test set to the
   verified device, notify it and wait at most 40 seconds for valid/active status.
   Record the canonical declaration and device-reported server token.
6. Read, without modifying,
   `/private/var/db/ManagedConfigurationFiles/LaunchRestriction/launch_restrictions.plist`.
   Repeat the baseline probes and capture the native `managedeventsd` log window.
   A successful `open` exit code alone is not evidence of application execution;
   check its actual process and native denial events.
7. Remove the assignment immediately in the controller's cleanup path, notify
   the device, and wait for its test declaration statuses to disappear. Delete
   the test set members and declarations. Verify the native policy is empty and
   every baseline probe succeeds again. Once the watchdog exits, delete its
   temporary credential file and audit the remaining assignments.

The watchdog is a fallback, not an additional test window. Stop after the probes;
do not add broad allow rules or change the system's core launch policy to recover.

## Recorded physical-host result

The bounded run on **19 September 2026** used source revision
`a7026bfaab6b0da45ab189ac31b1b855be22089b`, after PR #156 merged. It retained the
existing host server, whose build was
`v0.9.4-0.20260917091806-eb5208acb0c0+dirty`. That server predates Blueprint
administration, so delivery used its declaration/set API. The current Blueprint
path is covered separately by the automated regression test below.

| Probe | Signing observation | Before | While active | After removal |
| --- | --- | --- | --- | --- |
| Suspicious Package 4.5 | Developer ID; matching SigningID | Launched | Native denial; process absent | Launched |
| VLC 3.0.23 `--version` | Developer ID; unrelated SigningID and TeamID | Exit 0 | Exit 0 | Exit 0 |
| `/usr/bin/true` | Apple system executable | Exit 0 | Exit 0 | Exit 0 |
| Homebrew Python 3.14.7 | Ad-hoc; unrelated SigningID/CDHash; no TeamID | Exit 0 | SIGKILL, return code -9 | Exit 0 |
| Homebrew Git 2.55.0 | Ad-hoc; unrelated SigningID/CDHash; no TeamID | Exit 0 | SIGKILL, return code -9 | Exit 0 |

Native logs selected **Denylist** mode at **20:27:05 UTC**. Both declarations were
reported valid and active at **20:27:10 UTC**. Logs independently recorded denials
for the target, Python and Git. The captured effective policy contained only:

```json
{
  "DenyList": [
    {"SigningID": "com.mothersruin.SuspiciousPackageApp"}
  ]
}
```

Thus neither the server nor native policy translation had added an allow list or
another deny entry. The captured canonical payload also matched the authored
payload. The native policy cleared at **20:27:19 UTC**; status removal and successful
recovery probes followed. The final audit at **20:28:41 UTC** found zero test
assignments, zero test statuses and no remaining watchdogs. The temporary
credential file was deleted; user-approved enrollment was retained. No VM state
was changed.

Private evidence is retained under
`test-lab/local/application-identities/host/com.deploymenttheory.idhost.aa8d56ca261a/`.
It contains the original declaration, canonical payload, device statuses,
before/during/after probes, native policy snapshots, log window and cleanup audit.
Credentials, device identifiers, vendor executables and Apple binaries are not
part of this repository's reproduction fixture.

## Native signing-category check

Read-only inspection of this build's `/usr/libexec/managedeventsd` provides a
mechanism consistent with the observed split. In its arm64e policy evaluator,
after core exceptions and before custom identifier matching, a check rejects
Endpoint Security signing categories `INVALID`, `DEVELOPMENT`, `LOCAL_SIGNING`
and `NONE`. This is separate from the configured rule's `SigningState` qualifier.

The build-specific evidence is:

- Binary SHA-256:
  `1c30dc99d1b1823f0e650a9f451d38777ff2e0f1edcc996643a755caba87e3fc`.
- Evaluator entry: `0x1000041e8`. It reads `es_process_t.cs_validation_category`
  at offset `0xd0` and checks mask `0x489` at `0x1000043b4`–`0x1000043c4`, before
  the custom deny matcher at `0x1000045e4`.
- The selected mask bits are enum values 0, 3, 7 and 10. Their names and the
  structure offset were verified against the installed Endpoint Security SDK,
  including compiler static assertions. Apple's
  [validation category API](https://developer.apple.com/documentation/endpointsecurity/es_cs_validation_category_t)
  identifies this value as the signature validation policy applied to a binary.

This is an inference from static control flow corroborated by the live signing
controls. Runtime Endpoint Security category values were not captured, and the
development/local-signing categories were not separately exercised. The result
must not be generalized to every binary or future OS build. No daemon, system
policy file or signing identity was modified.

## Library and server regression

`TestAppSettingsDenyOnlyDelivery` exercises typed `AppSettings` authoring,
target-aware Blueprint compilation, set publication, assignment, manifest
advertisement and device declaration delivery. It compares the entire payload
against an explicit expected object and verifies the delivered server token.

Cases cover SigningID-only, CDHash-only, combined identifiers/path, and an explicit
`SigningState: All`. Omitted fields must remain absent, including AllowedBinaries;
the explicit qualifier must survive. These are serialization and delivery checks,
not simulations of native macOS enforcement. Run them with:

```sh
go test -race ./devicemanagement/mdmprotocol/ddm ./devicemanagement/mdmprotocol/ddm/blueprint
```

There is no supported library workaround established by this investigation.
Identity discovery and deterministic authoring remain valid; native isolated
execution control remains unproven on the affected build.

## Apple feedback draft

**Title:** macOS 27.0 (26A428): SigningID-only DDM DeniedBinaries also rejects
unrelated ad-hoc signed executables.

**Environment:** Supervised, enrolled physical Apple silicon Mac; existing MDM
enrollment; no other assigned binary restrictions. The declaration is valid and
active. The configuration fixture and activation above are sufficient to describe
the delivered policy independently of the identity discovery helper.

**Expected:** The matching app cannot launch; unrelated executables that passed
the baseline retain their behavior with no AllowedBinaries configured.

**Actual:** The matching Developer ID app is denied, and unrelated Homebrew Python
and Git are killed. An unrelated Developer ID app and Apple system executable
still run. Native logs show Denylist mode and denials for the unrelated binaries;
the effective native policy contains only the requested SigningID. Removal
restores all probes.

**Request:** Confirm whether the additional signature-category restriction is
intentional. If it is, document the behavior and supported scope of deny-only
rules; otherwise investigate enforcement before custom rule matching. The table,
payload, native policy snapshot, timestamps and build-specific analysis above
provide the reproduction evidence. Supply private device logs only after review.
This draft has not been submitted to Apple.
