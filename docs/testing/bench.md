# Testing through the reference server

The [bench](../../test-lab/README.md) shares `dmserver` configuration and runtime
with acceptance and end-to-end testing. Scenario functions are Go code independent
of `testing.T`; [the catalogue](bench-catalogue.md) is generated from those functions'
metadata. Configuration-specific scenarios start isolated server instances.

The [enrollment validation record](enrollment-validation.md) records automated and
live status for the enrollment-alignment branch. The earlier
[PR #12 validation record](bench-validation.md) describes the preceding APNs and
reference-bench implementation.

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
uses isolated SQLite databases and provisions the split roles itself. The older
container-based split regression remains packaging coverage in the existing CI job.

`make bench-docs` regenerates the catalogue. `make bench-docs-check` detects drift.
GitHub Actions uploads acceptance evidence even on failure. Credentials, raw APNs
tokens and bootstrap tokens are excluded from result reports.

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
