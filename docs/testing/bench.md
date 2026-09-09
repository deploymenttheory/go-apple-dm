# Testing through the reference server

The [bench](../../test-lab/README.md) shares `dmserver` configuration and runtime
with acceptance and end-to-end testing. Scenario functions are Go code independent
of `testing.T`; [the catalogue](bench-catalogue.md) is generated from those functions'
metadata. Configuration-specific scenarios start isolated server instances.

The [implementation validation record](bench-validation.md) records tested adapters,
the outstanding coverage gate and live prerequisites as of 9 September 2026.

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

The shared automated catalogue covers all existing scenario families and ordinary
app alert/background pushes. Reserved E2E-015/E2E-022 are not advertised as
implemented scenarios. Hardware-specific flows remain simulated unless a live
adapter is explicitly listed. Live execution currently covers the host app and
a previously enrolled device's DeviceInformation response.

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
