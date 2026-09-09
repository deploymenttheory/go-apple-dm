# Reference bench validation — 9 September 2026

Implementation branch: `feat/apns-certificate-workflow`. These results describe
the uncommitted implementation at validation time, not a published release.

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

## Coverage gate remains failing

`make coverage` reports **94.83% overall**, below the existing **95%** threshold.
The following packages also remain below the package threshold:

| Package | Coverage |
|---|---:|
| `appleplatformservices/push/apns` | 94.32% |
| `pki/pushcert` | 89.20% |
| `server/apppush` | 93.75% |
| `server/internal/app` | 93.46% |
| `server/internal/bench` | 69.21% |
| `server/internal/dmctl` | 85.33% |
| `server/internal/runtime` | 81.69% |
| `storage` | 94.59% |

The threshold and exemption list are unchanged. Passing functional tests does
not make this change ready for CI under the current coverage policy. Further
coverage work is required before that gate passes. Inspect `cover/merged.html`
and `cover/packages.txt` after reproducing the unit, contract and E2E targets.

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
