# Testing through the reference server

The [bench](../../test-lab/README.md) shares `dmserver` configuration and runtime
with acceptance and end-to-end testing. Scenario functions are Go code independent
of `testing.T`; [the catalogue](bench-catalogue.md) is generated from those functions'
metadata. Configuration-specific scenarios start isolated server instances.

Record results against the exact source revision, configuration and device/OS
in the PR. Automated checks and a successful run on one device establish only
their tested scope. Keep private evidence and credentials outside source control.

| Layer | Entry point | What it proves |
|---|---|---|
| Unit | `make test` | Component behavior and error handling |
| Contract | `make test-contract` (`test-storage` alias) | Interchangeable state/storage implementations obey the same interface |
| E2E | `make test-e2e` | Shared scenarios against embedded reference-server runtime, plus retained detailed regressions |
| Acceptance | `make test-acceptance` | The same shared scenarios against built `dmserver` processes, including split deployment |
| Live bench | `make bench-run` in an explicitly live workspace | Applicable behavior with real Apple credentials and devices |

`server/acceptance` is tagged `e2e` or `acceptance`. An empty `BENCH_DMSERVER` selects
the embedded runtime; `make test-acceptance` supplies the absolute built binary.
Both use `app.ParseEnv`, `app.Build` and the shared lifecycle. Process acceptance
also supplies `BENCH_DMCTL` and exercises init, start, status, scenario execution,
profile issuance, shutdown and restart through the built CLI. External services
remain fixtures, including in process acceptance: these runs do not establish
Apple interoperability.

The older `server/e2e` tests retain detailed component-level assertions, including
fake-clock backoff, persistence inspection, protocol faults, software-update gates
and identity replay. Their original IDs and assertions are preserved in
[e2e-scenarios.md](e2e-scenarios.md). The catalogue links those regressions to the
corresponding shared workflow. A shared scenario's pass does not imply every
internal assertion in its retained regression ran in that adapter. `test-e2e`
runs both. Contract tests remain direct interface tests, without a server wrapper.

## Coverage and evidence

Embedded runtimes retain their bound listeners while constructing fixtures and
applications. They serve those same sockets, so ephemeral port allocation does not
leave a close/rebind gap. Process adapters launch the ordinary binary, which binds
its own listener; use an explicit workspace address when a stable port is required.

The shared automated catalogue covers all existing scenario families and ordinary
app alert/background pushes. Reserved E2E-015/E2E-022 are not advertised as
implemented scenarios. Hardware-specific flows remain simulated unless a live
adapter is explicitly listed. Live execution covers the host app, service discovery and an enrolled device's
DeviceInformation response. LIVE-002 and LIVE-003 additionally require recorded
ACME/SCEP issuance and an acknowledged ProfileList on the installing Mac user's
exact channel, selected with `-user-id` (`BENCH_USER_ID` in make). LIVE-004 assigns
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
designated test Mac or VM. See [Blueprint acceptance on macOS 26](blueprints-macos26.md)
for the command, exact assertions and recorded scope.

The Mac can omit unchanged OS/build values from repeated status reports: status
subscriptions combine as a set union and reports are incremental. LIVE-004 checks
retained values against current inventory; it does not claim that every repetition
produced new OS/build status items. Initial receipt is recorded separately.

For a live server started directly, `dmctl bench run`, `profile` and `replace`
accept `-attach-url https://127.0.0.1:8443` (`BENCH_ATTACH_URL` in make). This uses
the existing workspace's CA and admin credential without supervisor state.
It requires an HTTPS origin and rejects redirects. Record the running server's
source and binary hashes alongside reports; the CLI's revision alone cannot
identify an independently started server.

Reports include stable scenario ID, mode, adapter, revision, status, start and
duration. Private JSON and JUnit files are written together. JUnit marks blocked
and unsupported scenarios as skipped, while an explicitly selected non-pass makes
the CLI fail. `all` selects the modes available in that workspace. CI must fail on
unexpected blocked or unsupported results in its selected automated suite.

The E2E database matrix retains SQLite and PostgreSQL. Contract suites exercise
configured SQL backends; `make testdb-up` prints their settings. Process acceptance
uses an isolated SQLite database for each unified server. Inventory learned
through MDM controls DDM delivery through the in-process adapter.

`make bench-docs` regenerates the catalogue. `make bench-docs-check` detects drift.
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
make bench-docs-check
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
2. Add or extend a catalogue scenario using normal administration and device APIs.
   Declare its configuration, modes and prerequisites.
3. Keep deterministic timing, storage and protocol edge assertions in focused
   unit/contract/regression tests when HTTP is not the appropriate observation point.
4. Run the shared scenario against the embedded runtime and built executable.
5. Update the design record, API/configuration documentation, generated catalogue
   and live runbook in the same change. Record unverified live prerequisites explicitly.

Do not add another daemon or script that assembles a competing server. Fixture
controllers belong to the bench supervisor, never to normal server routes.

## Enrollment alignment

E2E-027 checks service discovery and trust. E2E-028 through E2E-031 exercise
successful and failed SCEP/ACME replacement against the shared runtime. The
storage contract tests cover commit ordering, expiry, cancellation and concurrent
creation while checking queue, escrow and user-state preservation. Pre-release
SQL fixtures build all tables from each backend's initial schema.

Use `make bench-enrollment-preflight`, `bench-trust`, `bench-profile` and
`bench-replace` for operator preparation. Follow the [Mac enrollment runbook](../operations/mac-enrollment-testing.md)
for device installation and live acceptance. Simulator failures validate server
recovery; device-side rollback and ADE activation need their own live evidence.
