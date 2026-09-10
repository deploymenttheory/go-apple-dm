# Reference bench validation — 9 September 2026

For the subsequent enrollment changes, see the
[10 September enrollment validation](enrollment-validation.md). The passing gate
below applies to PR #12, not to that later branch.

Implementation branch: `feat/apns-certificate-workflow`. These results describe
the implementation and coverage follow-up on PR #12, not a published release.

## Passing checks

| Check | Result |
|---|---|
| `make test` | Both modules pass with race detection, shuffle and coverage |
| `make test-contract` | Passes with local PostgreSQL and MySQL configured; app credential contracts also cover memory and SQLite |
| `make test-e2e` | Shared embedded-runtime scenarios and retained detailed regressions pass on SQLite |
| `make test-acceptance` | 27 shared simulated scenarios pass against built processes, including split roles; built CLI lifecycle test also passes |
| `make bench-docs-check` | Catalogue matches executable metadata |
| `make verify` | Schema regeneration verification passes |
| Repository lint, both modules | Zero issues with automatic fixes disabled |
| APNs lifecycle repetition | 30 race-enabled, coverage-instrumented runs pass after fixing active HTTP/2 connection retirement |
| Native host app | `test-lab/host-app/build.sh --unsigned` compiles successfully |
| Existing local workspace | Live-mode startup, status and orderly shutdown pass |

Process scenario evidence is written to `cover/acceptance/process/{all,split}/`
as JSON and JUnit. The CLI lifecycle test covers init, start, status, scenario
execution, profile issuance, shutdown and restart. Tests cover encrypted-record
tampering, credential pagination, explicit TLS trust, registration mismatch and
the requirement for a correlated app receipt.

The older Docker-packaged split regression requires its separate container
configuration and was not exercised locally in this validation. The existing CI
job retains it; local process acceptance exercised both native split roles.

## Coverage gate passes

`make coverage` reports **95.87% overall** against the unchanged **95%** threshold.
Every non-exempt package meets its package threshold. The packages previously
below the gate now report:

| Package | Coverage |
|---|---:|
| `appleplatformservices/push/apns` | 96.07% |
| `pki/pushcert` | 95.31% |
| `server/apppush` | 95.00% |
| `server/internal/app` | 95.25% |
| `server/internal/bench` | 95.18% |
| `server/internal/dmctl` | 95.00% |
| `server/internal/runtime` | 98.48% |
| `storage` | 97.30% |

This measurement merges fresh race-enabled unit, PostgreSQL/MySQL storage
contract, and SQLite E2E/embedded-acceptance coverage. Thresholds and exemptions
are unchanged. Reproduce with `make test`, `make test-contract`, `make test-e2e`,
and `make coverage`; inspect `cover/merged.html` and `cover/packages.txt`.

Added regressions exercise malformed certificates and CSRs, invalid TLS key
usage, CLI signing/import and bench commands, operator API validation, missing
and unwritable workspace files, credential preservation, supervisor process
exits, ordered shutdown, live receipt/acknowledgement requirements, and interrupted
or incomplete scenario exchanges. The tests use temporary identities and local
fixtures.

The outage tests exposed negative enrollment checks that accepted unrelated
transport errors as proof of rejection. ACME, OTA and user-channel checks now
require an explicit protocol rejection. The shared runtime shutdown coordinator
also has direct tests for HTTP draining, worker failure and deadlines.

## Live prerequisites

The supplied Desktop certificate was inspected as an ordinary app APNs
certificate for `com.weaveplatform.deviceweave`, not an MDM certificate. Its
certificate validity ends on 9 October 2027. Actual Apple delivery remains
unverified.

`test-lab/local/bench.json` is initialized in live mode. All seven existing
server identity/secret files were preserved. The local server was stopped after
the startup check; start it again with `make bench-up`.

The workspace still needs:

- The matching app private key at `app/push.key`, or a replacement certificate
  issued for the preserved replacement CSR/key; a valid signing/provisioning
  setup and exported `app/registration.json` are also needed.
- The customer MDM certificate at `mdm/push.pem`, paired with the retained key,
  before MDM enrollment and wake testing.
- The vendor signing certificate chain at `vendor/chain.pem` for the local
  vendor CSR-signing workflow.

No live APNs notification was sent and no device enrollment or host trust change
was performed. See the [runbook](../../test-lab/README.md) for the next steps and
the separate app and MDM certificate workflows.
