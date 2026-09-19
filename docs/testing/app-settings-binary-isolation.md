# App Settings binary controls and signing restrictions on macOS

On the tested physical Mac running **macOS 27.0 (26A428)**, a deny rule for one
application also prevented unrelated ad-hoc signed executables from starting.
Unrelated Developer ID and Apple signed controls continued to run. Removing the
declaration restored all controls. These observations match Apple's documented
signing restrictions when binary controls are enabled. The reference server
delivered the intended deny-only payload.

This report resolves the interpretation of the
[application identity acceptance result](application-identities-macos.md#physical-host-app-settings-enforcement).
The observed deny-mode behavior is consistent with the platform contract; it does
not establish every binary-control variant or a successful DDM app installation.

## Protocol expectation

Apple's [App Settings documentation](https://developer.apple.com/documentation/devicemanagement/appsettings)
permits a `DeniedBinaries` entry containing only `SigningID`.
The [allowed settings dictionary](https://developer.apple.com/documentation/devicemanagement/appsettingsallowedobject)
describes denials by matching binary identifiers, with all supplied identifiers
required to match. An allow list restricts execution when `AllowedBinaries` is
present. The [binary identifier dictionary](https://developer.apple.com/documentation/devicemanagement/appsettingsallowed_binaryidentifierobject)
defines `SigningState` as a matching qualifier with default `All`.

Apple's [deployment guide](https://support.apple.com/guide/deployment/allow-and-deny-apps-and-binaries-dep001044b08/1/web/1.0),
published 17 September 2026, explains an independent restriction: enabling binary
controls rejects unsigned, ad-hoc signed and development-signed executables,
including with an explicitly empty allow or deny list. macOS also applies its
core policy and the configured identifier rules. `SigningState` qualifies a rule;
it does not disable the baseline signing restriction.

Signature integrity, signing category and execution permission are separate facts.
An ad-hoc signature can verify successfully while the binary remains ineligible
under this policy. Acceptance must distinguish eligible signed controls from
ad-hoc controls instead of expecting every unrelated executable to run.

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

Use an explicitly designated supervised physical test Mac. This procedure
temporarily prevents the ad-hoc signed Homebrew controls from launching.

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
   check its actual process and native denial events. Expect the matching target
   to be denied, the unrelated Developer ID and Apple controls to run, and the
   ad-hoc controls to be denied. Remove the assignment after these observations
   even if a result differs from the expectation.
7. Remove the assignment immediately in the controller's cleanup path, notify
   the device, and wait for its test declaration statuses to disappear. Delete
   the test set members and declarations. Verify the native policy is empty and
   every baseline probe succeeds again. Once the watchdog exits, delete its
   temporary credential file and audit the remaining assignments.

The watchdog is a fallback, not an additional test window. Stop after the probes;
do not add broad allow rules or change the system's core launch policy to recover.
Withdraw the test configuration for cleanup; an empty binary list is not a
substitute for removing the policy.

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

The target denial, eligible control execution, ad-hoc control denials and recovery
are consistent with the documented policy. Empty-list behavior, development-signed
and unsigned controls, allow mode and managed-app exceptions were not separately
tested. Their documentation must not be mistaken for additional native results.

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

Identity discovery and deterministic authoring remain valid. A policy intended to
block one app while preserving arbitrary ad-hoc tools cannot be represented by
these DDM binary controls. The library preserves the author's selected identifiers
and constraints; it does not override macOS's execution policy.

## Resolved interpretation

The earlier interpretation treated Python and Git as controls that should remain
executable regardless of their signing category. That expectation was incorrect.
Apple's deployment guide explains their denials, and the recorded results support
that explanation. The original observations and private evidence remain unchanged.

The proposed Apple defect report is retired without submission. This correction
does not require a new physical test or an enforcement workaround. It establishes
the meaning of the existing evidence, without claiming coverage of the untested
variants listed above.
