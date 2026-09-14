# Enrollment alignment validation — 10 September 2026

Implementation branch: `feat/enrollment-profile-alignment`, based on merged PR #12.
This historical report covers service discovery, enrollment profiles and controlled
identity replacement as tested on 10 September. For subsequent physical-device
results, see the [13 September real Mac lifecycle record](../reviews/real-mac-lifecycle-2026-09-13.md).
That record establishes ACME and SCEP enrollment, APNs-triggered inventory,
installing-user commands, DDM, identity replacement, trust rollover and SQLite
recovery on the tested candidates, with their acceptance boundaries recorded there.

## Automated checks

| Check | Result |
|---|---|
| `make test` | Passes in both modules with race detection, shuffle and coverage |
| `make test-contract` | Passes with local PostgreSQL and MySQL configured, alongside memory and SQLite contracts |
| `make test-e2e` | Shared embedded-runtime scenarios and retained detailed regressions pass on SQLite |
| `make test-acceptance` | 31 shared simulated scenarios and the split deployment scenario pass against built processes; CLI lifecycle also passes |
| `make bench-docs bench-docs-check` | Generated catalogue matches executable metadata |
| `make verify` | Passes |
| Repository lint, both modules | Zero issues with golangci-lint v2.13.2 built for Go 1.27 |
| `make coverage` | Fails the per-package gate; overall coverage is 95.55% |

The coverage threshold and exemptions remain unchanged. Five packages remain
below the required 95%:

| Package | Coverage |
|---|---:|
| `server/internal/app` | 92.82% |
| `server/internal/bench` | 91.83% |
| `server/internal/dmctl` | 93.71% |
| `server/service` | 94.69% |
| `server/sqlstore/sqlcommon` | 93.91% |

These results merge the final unit, configured SQL contract and SQLite E2E
coverage. The change remains a draft pending the missing coverage. The passing
coverage gate recorded for PR #12 does not apply to this branch.

New shared scenarios cover service discovery and both successful and failed
replacement using SCEP and ACME. Storage contracts exercise commit ordering,
concurrent attempt creation, cancellation, expiry and preservation of queued work,
escrow and user channels. PostgreSQL and MySQL also run the replacement contract
with encryption enabled. Packages that reset the shared integration database run
serially to prevent concurrent schema resets.

Replacement storage is part of each backend's `0001_init.sql`. The application is
pre-release and has no existing database upgrade requirement.

## Subsequent real Mac validation

The enrollment and push-certificate gaps recorded on 10 September were superseded
by physical-Mac testing on 13 September. The [lifecycle record](../reviews/real-mac-lifecycle-2026-09-13.md)
reports LIVE-002 passing for ACME and LIVE-003 passing for SCEP, including
APNs-triggered DeviceInformation and ProfileList on the exact installing-user
channel. It also records LIVE-004, successful identity replacement, controlled
issuance refusal, issuer/HTTPS trust rollover and recovery of the enrolled Mac
from a consistent SQLite checkpoint.

Those results apply to the candidates identified in that record; its requirement
to repeat final acceptance on the reviewed commit remains explicit. They do not
establish ADE Setup Assistant activation or make the automated results above
current. Use the [manual Mac enrollment runbook](../operations/mac-enrollment-testing.md)
for the live acceptance procedure.
