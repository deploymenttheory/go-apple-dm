# Testing through the reference server

The [lab](../../test-lab/README.md) shares `dmserver` configuration and runtime
with acceptance and end-to-end testing. It is the single acceptance entry point:
`dmctl lab` owns workspaces, module selection, execution and evidence. Module steps
are Go code independent of `testing.T`; [the catalogue](lab-catalogue.md) is
generated from their metadata. Configuration-specific modules start isolated server
instances.

Record results against the exact source revision, configuration and device/OS
in the PR. Automated checks and a successful run on one device establish only
their tested scope. Keep private evidence and credentials outside source control.

| Layer | Entry point | What it proves |
|---|---|---|
| Unit | `make test` | Component behavior and error handling |
| Contract | `make test-contract` (`test-storage` alias) | Interchangeable state/storage implementations obey the same interface |
| E2E | `make test-e2e` | Shared modules against the embedded reference-server runtime, plus retained component regressions |
| Acceptance | `make test-acceptance` | The same shared modules against built `dmserver` processes with unified device management |
| Live lab | `make lab-run` in an explicitly live workspace | Applicable behavior with real Apple credentials and devices |

`server/acceptance` is tagged `e2e` or `acceptance`. An empty `LAB_DMSERVER` selects
the embedded runtime; `make test-acceptance` supplies the absolute built binary.
Both use `app.ParseEnv`, `app.Build` and the shared lifecycle. Process acceptance
also supplies `LAB_DMCTL` and exercises init, start, status, module execution,
profile issuance, shutdown and restart through the built CLI. External services
remain fixtures, including in process acceptance: these runs do not establish
Apple interoperability.

The `server/e2e` tests are component regression tests, not an acceptance layer.
They retain detailed component-level assertions, including
fake-clock backoff, persistence inspection, protocol faults, software-update gates
and identity replay. Their original IDs and assertions are preserved in
[e2e-scenarios.md](e2e-scenarios.md). The catalogue links those regressions to the
corresponding shared workflow. A shared module's pass does not imply every
internal assertion in its retained regression ran in that adapter. `test-e2e`
runs both. Contract tests remain direct interface tests, without a server wrapper.

## Modules, targets and evidence

A module is one maintained check: an ordered list of steps with a theme, a
lifecycle stage, the workspace modes it needs and the target capabilities it uses.
Stages order a run for one target, from preflight and enrollment through inventory,
configuration, apps and unenrollment. `dmctl lab list` prints the catalogue;
`-format markdown` regenerates [the catalogue document](lab-catalogue.md).

Modes and targets vary independently. A mode selects the service environment:
`simulated` uses local Apple-service fixtures, `live` uses the operator's Apple
credentials. A target supplies the device side: the protocol simulator, or a real
device identified by `-device-id` and, for user-channel modules, `-user-id`. A
module that does not apply to the selected mode or target is reported as
`unsupported` with the reason, never as a pass. A module marked as a gate blocks
later modules for that target when it does not pass, so the lab never reports
downstream results it could not have observed. Modules tagged destructive run only
with `-destructive`.

Every run writes `results.json`, `junit.xml` and a self-contained `report.html` to
its evidence directory. Results record the module, target, stage, steps, status and
duration. `dmctl lab report -run DIR` rerenders the HTML from existing results.

## Server adapters and the container stack

`dmserver` runs in one of three ways. The embedded runtime and the built process are used
by the automated suites; the container stack is the one a device talks to.

| Adapter | Selected by | Used for |
|---|---|---|
| `inprocess` | an empty `LAB_DMSERVER` | `make test-e2e` |
| `process` | the workspace default | `make test-acceptance`, local simulated runs |
| `docker` | `lab init -adapter docker` | live device acceptance |

The container adapter runs live workspaces only: simulated Apple-service fixtures live in
the lab process on the host, where a container cannot reach them. It builds the repository
`Dockerfile` `runtime` target and starts
[the lab stack](../../deploy/lab/README.md): `dmserver` with the workspace's `mdm/`
directory bind-mounted at `/data`, and a read-only static HTTPS origin for files a device
fetches. `dmctl lab up` writes the container environment, translating workspace paths to
their container locations, and waits for both services to become healthy. `lab down`
removes the containers and leaves the bind-mounted state.

```sh
make lab-init LAB_MODE=live LAB_ADAPTER=docker LAB_LISTEN=127.0.0.1:8443 \
  LAB_HOSTS=mdm.lab.test,192.168.64.1
make lab-up
```

`LAB_HOSTS` names the server for devices. Those names go into the HTTPS leaf and into
`DM_PUBLIC_URL`, while administration keeps using loopback. `make lab-tls LAB_HOSTS=…`
reissues the leaf from the retained lab CA when the address changes, so a device that
already trusts the CA does not have to install the trust profile again. Ports are
published on the workspace's `Bind` address, loopback unless it is set, so nothing is
exposed on the LAN by default.

`lab doctor` reports the container prerequisites: the docker command, a reachable daemon,
the compose file, free space and the published addresses. It also reports why a live
workspace is not yet serving enrollment: those routes stay unmounted until the MDM push
certificate supplies a topic.

`make lab-tools` fetches the guestweave CLI at its latest release tag, builds it and
verifies that it carries the virtualization entitlement. `GUESTWEAVE_REF` selects another
ref. The repository is private, so this needs git or `gh` credentials.

## Coverage and evidence

Embedded runtimes retain their bound listeners while constructing fixtures and
applications. They serve those same sockets, so ephemeral port allocation does not
leave a close/rebind gap. Process adapters launch the ordinary binary, which binds
its own listener; use an explicit workspace address when a stable port is required.

The shared automated catalogue covers all existing module themes and ordinary
app alert/background pushes. Reserved E2E-015/E2E-022 are not advertised as
implemented coverage. Hardware-specific flows remain simulated unless a live
adapter is explicitly listed. Live execution covers the host app, service discovery and an enrolled device's
DeviceInformation response. LIVE-002 and LIVE-003 additionally require recorded
ACME/SCEP issuance and an acknowledged ProfileList on the installing Mac user's
exact channel, selected with `-user-id` (`LAB_USER_ID` in make). LIVE-004 assigns
a unique OS/build subscription and activation, requires a fresh report showing
active, valid declarations and DDM values matching fresh DeviceInformation, then
removes its assignment and declarations and
waits for the device's status to reflect removal. Obtain operator approval before
running this live declaration test.

LIVE-005 and LIVE-006 exercise Blueprint publication on a macOS 26 device and its
MDM-enabled user channel. They require fresh native declaration status and
APNs-triggered `ProfileList` responses for conditional activation, replacement of
an uploaded configuration profile, clearing and republishing while preserving the
assignment, unassignment, reassignment and deletion. LIVE-005 also checks that
`LegacyProfile.ProfileAssetReference` is withheld on macOS 26 while a compatible
configuration in the same Blueprint reaches the device. Run these only against a
designated test Mac or VM. See [Blueprint acceptance](#blueprint-acceptance)
for the command, exact assertions and platform limits.

The Mac can omit unchanged OS/build values from repeated status reports: status
subscriptions combine as a set union and reports are incremental. LIVE-004 checks
retained values against current inventory; it does not claim that every repetition
produced new OS/build status items. Initial receipt is recorded separately.

For a live server started directly, `dmctl lab run`, `profile` and `replace`
accept `-attach-url https://127.0.0.1:8443` (`LAB_ATTACH_URL` in make). This uses
the existing workspace's CA and admin credential without supervisor state.
It requires an HTTPS origin and rejects redirects. Record the running server's
source and binary hashes alongside reports; the CLI's revision alone cannot
identify an independently started server.

Reports include the stable module ID, mode, adapter, revision, status, start and
duration. Private JSON and JUnit files are written together. JUnit marks blocked
and unsupported modules as skipped, while an explicitly selected non-pass makes
the CLI fail. `all` selects the modes available in that workspace. CI must fail on
unexpected blocked or unsupported results in its selected automated suite.

The E2E database matrix retains SQLite and PostgreSQL. Contract suites exercise
configured SQL backends; `make testdb-up` prints their settings. Process acceptance
uses an isolated SQLite database for each unified server. Inventory learned
through MDM controls DDM delivery through the in-process adapter.

`make lab-docs` regenerates the catalogue. `make lab-docs-check` detects drift.
GitHub Actions uploads acceptance evidence even on failure. Credentials, raw APNs
tokens and bootstrap tokens are excluded from result reports.

## Reproducing checks

Initialize the pinned submodules and use the declared Go toolchain. Start the SQL
fixtures with `make testdb-up`, export the printed PostgreSQL/MySQL DSNs, then run:

```sh
make verify
make test
make test-contract
make test-e2e
E2E_STORE=postgres make test-e2e
make test-acceptance
make lab-docs-check
make coverage
```

SQL packages that reset shared integration databases run serially through the
Makefile. A skipped dependency
is a limit on evidence, not a pass. Coverage merges emitted profiles; remove stale
profiles before assembling a measurement and inspect `cover/packages.txt` and
`cover/merged.html`. The gate remains 95% overall and per non-exempt package.

Live app push needs a matching app certificate/key, signing/provisioning and a
registration exported by the host app. MDM enrollment needs the separate customer
MDM push certificate and retained key. Vendor CSR signing needs the vendor chain.
Use the [lab runbook](../../test-lab/README.md) for the respective file layouts;
APNs acceptance alone does not prove device or app receipt.

## Apple management feature checks

Run focused helper, admin and CLI suites without Apple credentials:

```sh
go test -race ./devicemanagement/mdmprotocol/... ./devicemanagement/appleplatformservices/appsbooks/...
go test -race ./server/replycerts/... ./server/internal/app/... ./server/internal/dmctl/...
```

The tests check independent JWT signatures, PBKDF2 and bypass-code vectors,
pre-generated CMS fixtures, manifest digests, pagination above 1,000 records,
authorization, channel isolation and encrypted identity retention across restart.
Apps and Books tests use controlled HTTP services. These establish local contracts,
not acceptance by Apple services or every device family.

| Live check | Prerequisites and acceptance boundary |
|---|---|
| DDM inspection | Enrolled device and current inventory. Traverse values/errors/reports with `-limit 1 -all`, compare against a large page and test a prefix. An empty errors page does not establish nonempty error pagination. |
| Profile lint/install/remove | A benign profile and an eligible target. Lint, install, confirm its identifier with ProfileList, remove, and confirm absence. Acceptance applies to that payload and target only. |
| Managed Apple Account JWT | Apple-registered ADE identity and supported GetToken exchange; local signature verification cannot prove registration. |
| ADE password hashes | An ADE-created administrator and its GUID; verify AccountConfiguration and SetAutoAdminPassword separately. |
| FileVault escrow | FileVault enabled, an existing personal recovery key, bootstrap token and no conflicting escrow ownership. Follow the [escrow workflow](../operations/protocol-helpers.md#automatic-filevault-encryption-certificates); check acknowledgement, recover the CMS output with the retained recipient, persist the secret encrypted, and repeat decryption after restart. |
| FileVault retrieval/rotation/unlock | SecurityInfo needs its enrollment access right. Local `/var/db/FileVaultPRK.dat` extraction does not prove remote retrieval. Explicit RotateFileVaultKey needs unlock credentials; disk unlock is a separate test. Removing the escrow profile does not reverse rotation. Retain the replacement key and recipient in a verified post-rotation backup. |
| Activation Lock | Eligible organization/device and retained bypass code; enabling with a hash and unlocking with the code require separate acceptance. |
| Package/app manifests | Distributable signed assets hosted at the hashed HTTPS URL and eligible macOS/iOS/iPadOS targets; local digest checks do not establish installation. |
| Apps and Books | Location token, ownership, licenses, suitable devices/users and an authenticated notification receiver. Verify user association where required, asynchronous completion and installation separately. |

Record the running source and binary hashes, OS/hardware/enrollment mode, command
results, cleanup and untested operations in the PR. Keep device identifiers,
credentials and recovery material in private artifacts. Repeat acceptance for the
revision being deployed; no previous observation establishes universal compatibility.

## Graduating a spike

1. Put reusable protocol/client behavior in the library and server composition or
   operational APIs in the reference server.
2. Add or extend a catalogue module using normal administration and device APIs.
   Declare its configuration, modes and prerequisites.
3. Keep deterministic timing, storage and protocol edge assertions in focused
   unit/contract/regression tests when HTTP is not the appropriate observation point.
4. Run the shared module against the embedded runtime and built executable.
5. Update the design record, API/configuration documentation, generated catalogue
   and live runbook in the same change. Record unverified live prerequisites explicitly.

Do not add another daemon or script that assembles a competing server. Fixture
controllers belong to the lab supervisor, never to normal server routes.

## Enrollment alignment

E2E-027 checks service discovery and trust. E2E-028 through E2E-031 exercise
successful and failed SCEP/ACME replacement against the shared runtime. The
storage contract tests cover commit ordering, expiry, cancellation and concurrent
creation while checking queue, escrow and user-state preservation. Pre-release
SQL fixtures build all tables from each backend's initial schema.

Use `make lab-preflight`, `lab-trust`, `lab-profile` and
`lab-replace` for operator preparation. Follow the [Mac enrollment runbook](../operations/mac-enrollment-testing.md)
for device installation and live acceptance. Simulator failures validate server
recovery; device-side rollback and ADE activation need their own live evidence.

## Blueprint acceptance

The [live Blueprint modules](../../server/lab/live_blueprints.go)
exercise macOS 26 device (LIVE-005) and installing-user (LIVE-006) channels. They
use native DDM reports, real APNs wakes and acknowledged `ProfileList` commands;
they are unsupported in simulated mode. LIVE-006 requires the installing user to
be logged in and MDM-enabled. The current runner's OS restriction is part of its
acceptance contract, not the full platform availability of Blueprint compilation.

### Assertions

Each module uses a unique Blueprint identifier, profile identifier and UUID.
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

Deferred cleanup runs with its own deadline even if the module fails or its
context expires. Cleanup errors fail the module. Uploaded immutable profile
revisions and retained declaration versions remain available to administrators;
the temporary Blueprint, its assignments and its installed profile are removed.

### Run

Build the working tree and use an explicitly live lab workspace containing the
server's CA certificate and admin credential. The server must already be running
with DDM status subscriptions enabled and an HTTPS public URL reachable by the VM.

```sh
make lab-build
test-lab/local/bin/dmctl lab run \
  -workspace /path/to/live-workspace \
  -attach-url https://your-mdm-server.example \
  -modules blueprints \
  -device-id DEVICE_ID \
  -user-id INSTALLING_USERS_GENERATED_UID \
  -revision SOURCE_REVISION \
  -report-dir /path/to/private/results
```

Use `-modules LIVE-005` or `-modules LIVE-006` to run one channel. The user ID is
the local account's `GeneratedUID`; the module builds the complete user enrollment
identity with its parent device ID. The user assignment and status requests include
that parent explicitly.

The report directory contains `results.json` and `junit.xml`. Phase evidence lives
under the workspace's `evidence/blueprints-live-*` directories: inventory,
publication records, configuration profile upload metadata, declaration status,
decoded `ProfileList` responses and the compatibility result. Record the running
server and CLI binary hashes, source revision and working-tree file hashes alongside
these files. All evidence directories are private and remain outside source control.

Apple defines the [LegacyProfile declaration](https://developer.apple.com/documentation/devicemanagement/legacyprofile)
and [ProfileList response](https://developer.apple.com/documentation/devicemanagement/profilelistcommand).
These checks prove profile installation/removal through `ProfileURL`; reading the
managed preference in an application is a separate behavior check. Native signed
profiles, OS 27 asset-reference delivery and other SQL backends need their own
acceptance evidence. No past run establishes a pass for a new source revision.
