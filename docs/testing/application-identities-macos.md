# Application identity acceptance on macOS

This check compares portable artifact discovery with applications installed on a
designated macOS test device. It exercises the
[authoring API](../operations/application-identities.md), preserves the original
artifact bytes and uses Apple's native signature tools as an independent oracle.
The test account needs installation rights. Installed-application MDM inventory
also requires that permission in the enrollment profile.

## Procedure

1. Build the candidate server and record its binary SHA-256, source revision,
   dependency versions and production source hashes. Verify the selected device's
   hardware identity, OS/build and enrollment before sending commands.
2. Obtain a signed vendor PKG and a vendor DMG containing an app. Record the
   source URL, exact version, SHA-256 and native package-signature result. Retain
   the original bytes privately; do not commit vendor binaries or credentials.
3. Upload each artifact to `POST /admin/v1/authoring/app-identities/artifacts`.
   Assert successful responses, exact SHA-256/size, expected bundle ID and all
   executable architectures. Review incomplete reports and issues explicitly.
4. Install those exact bytes on the test device. A PKG can use
   `InstallEnterpriseApplication` with a SHA-256 manifest; a DMG app can be copied
   from a read-only mount using the vendor's installation instructions. A native
   `installer` control run must be recorded separately from MDM delivery.
5. Confirm the app exists at its installed location and is visible in Finder.
   Read its `Info.plist` and run `codesign --verify --strict --all-architectures`.
   For every architecture in the portable report, run
   `codesign -d --verbose=4 --arch ARCH` against the installed main executable.
   Compare `Identifier`, `TeamIdentifier` and `CDHash` with the report. An MDM
   acknowledgment or installer receipt alone does not prove an app is present.
6. Populate an ordinary `AppSettings` payload with explicitly selected hashes.
   Validate it for a supported target and verify that an unsupported target is
   rejected. Publish an unassigned Blueprint, repeat with `If-Match`, verify the
   revision is unchanged, then delete it. Do not assign binary-control rules to
   a working Mac as part of this identity test.
7. Retain the reports, native output and cleanup results privately. Leave test
   apps available if the operator wants to inspect them. Remove temporary mounted
   images and test Blueprint state.

## Recorded run: 19 September 2026

The Guestweave VM `macos26-blueprints` ran macOS **26.6.2 (25G83)**. Its existing
enrollment and isolated SQLite database were retained. A fresh MDM
`DeviceInformation` command was acknowledged. Server production Go sources were
subsequently committed as `a5d404f`; the candidate was built before that commit.
Its binary SHA-256 was
`34f3f6c7a202bf137210a786fc957785ebf016d40a29eebbd1800e7d6ed54a70`.
The initial library API came from
`v0.7.4-0.20260919184614-68907f4c720a`.

| Artifact | Version/build | Installed bundle ID | Result |
| --- | --- | --- | --- |
| [Firefox PKG](https://www.firefox.com/en-US/browsers/enterprise/) | 156.0 / 15626.9.9 | `org.mozilla.firefox` | Native installer control installed the app; both architecture identities matched. |
| [Suspicious Package DMG](https://www.mothersruin.com/software/SuspiciousPackage/update.html) | 4.8 / 1457 | `com.mothersruin.SuspiciousPackageApp` | Copied from a read-only DMG; both architecture identities matched. |

Both artifact API responses were HTTP 200 and complete. Both installed apps
passed native strict signature verification for all architectures. The operator
also observed Firefox in the guest's Applications folder.

| Artifact | SHA-256 |
| --- | --- |
| Firefox PKG | `74ca97a7d2350d7dba62df62767c1d9d3f2bd3aeccef2e5129f2377c993ee386` |
| Suspicious Package DMG | `cbe17b1a1c1d6137e8f36443622863773709d415c0aeec68e41b29ce3fad297f` |

| App | Architecture | Matching CDHash |
| --- | --- | --- |
| Firefox | x86_64 | `e394bc6cef0dc741d8c7f02084337976cc2d9da5` |
| Firefox | arm64 | `98ba8c3ae09742b2d2d4518c09a8084217e23560` |
| Suspicious Package | x86_64 | `17960a0a48bb22368d261fabefcaf068dd0a22fc` |
| Suspicious Package | arm64 | `61a7409104fdb6464bc50fc377445752813846b4` |

The selected hashes validated for macOS 27; validation rejected macOS 26 because
the binary-control fields require the later target. Publication and identical
republication preserved the Blueprint revision. The unassigned test Blueprint
was deleted. No binary-control configuration was applied to the VM.

Two additional checks did not establish success:

- The initial Firefox `InstallEnterpriseApplication` command was acknowledged,
  downloaded the SHA-256-pinned package and wrote an installer receipt, but
  `/Applications/Firefox.app` was absent. The later native `installer` control
  installed the same bytes successfully. This run does **not** establish a
  successful first-time Firefox MDM install; its cause remains unresolved.
- `InstalledApplicationList` returned `MCMDMErrorDomain` error 12007 because the
  existing enrollment lacked that access right. Filesystem inspection and native
  signing checks establish the recorded presence and identity comparison.

Private evidence is retained under `test-lab/local/application-identities/`,
including artifact discovery, native comparison, authoring responses, installer
logs and a manifest of source and binary hashes. Credentials and device identity
details remain outside source control. These checks establish artifact-to-installed
identity parity and authoring behavior, not macOS 27 binary-control enforcement.

## Released readers

Discovery and installed-app comparisons were repeated with `go-macos-pkg v0.6.0`
and `go-apfs-v2 v0.3.1`. Both artifacts again returned complete reports, and both
architecture hashes, signing IDs and team IDs matched the installed apps. The
reader source files match those used in the initial run. The final server pins
root library `v0.7.4-0.20260919192617-bee5875be93c`; its rebuilt binary SHA-256 is
`2c10a986bdf4effbbcb71df02c6c09e193d711a3017da8a7e2a1b23678d65302`.
The utility and server authoring tests and standalone module installation/process
acceptance also pass with these released dependencies.

## Physical-host App Settings enforcement

An explicitly authorized follow-up on 19 September 2026 tested the physical host,
running macOS **27.0 (26A428)**. Hardware identity matched its existing enrollment;
the reference server reported a supervised device target and no assigned test
declarations. The existing host server and enrollment were preserved. That server
predates the Blueprint API, so this check used its declaration/set administration
API and the current Go authoring helper.

`appidentity.Inspect` inspected the host's **Suspicious Package 4.5**. Both
architectures had valid Developer ID signatures. The helper's results populated
and validated a typed `ddm.AppSettings` payload containing only `DeniedBinaries`.
Each entry combined CDHash, SigningID, TeamID and the application's
`Contents/MacOS/` path prefix. No allow list or team-wide rule was supplied.
A native shell/curl watchdog was armed to remove the assignment after 60 seconds,
independently of the Python test controller.

| Check | Observation |
| --- | --- |
| Before assignment | Suspicious Package launched; an unrelated Python control exited 0. |
| Declaration delivery | The Mac reported the configuration and activation valid and active at 20:06:51 UTC. |
| Intended restriction | Suspicious Package did not launch; native `managedeventsd` logs recorded `DENIED exec` for its executable. |
| Isolation control | The unrelated Python launch was also denied by `managedeventsd` and terminated with SIGKILL. |
| Removal | The test declarations disappeared from device status; Suspicious Package and Python both ran again by 20:07:05 UTC. No test declarations remained assigned. |

Reconstructing the declaration **without assignment** produced the same server
token the device had reported active. Its canonical payload matched the authored
deny-only payload exactly; the server had not introduced an allow list. The
temporary unassigned record was deleted after comparison.

**Result: DDM delivery passed; isolated binary enforcement failed.** This
reproduces the [earlier physical-host isolation failure](macos27-live-validation.md)
with a vendor app and additional identifier/path constraints. The result does not
establish a successful app-control workflow. A separate
[SigningID-only isolation test](app-settings-binary-isolation.md) narrows the
unrelated failures to ad-hoc signed controls and identifies a native signing-category
check consistent with that behavior. All controls recovered after removal.

Private evidence is under `test-lab/local/application-identities/host/`, including
helper output, the exact typed payload, before/during/after execution probes,
device status, reconstructed canonical payload/token, native logs and removal
records. No device identifiers or administrative credentials are committed.
