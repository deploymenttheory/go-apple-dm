# Enrollment alignment validation — 10 September 2026

Implementation branch: `feat/enrollment-profile-alignment`, based on merged PR #12.
This report covers service discovery, enrollment profiles and controlled identity
replacement in the reference server. It does not establish real Mac enrollment.

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

## Real Mac status

The development Mac reports `MDM enrollment: No` and `Enrolled via DEP: No`.
No real Mac has completed enrollment, replacement or an APNs-triggered inventory
query against this implementation.

The supplied certificate is an ordinary app-push certificate for
`com.weaveplatform.deviceweave`. The lab still needs an MDM push certificate
matching its retained private key. Its customer CSR and vendor-signing CSR have
valid self-signatures and match their respective local keys. Neither is the
vendor-signed request accepted by Apple's Push Certificates Portal.

The Apple-issued MDM Vendor CSR Signing Certificate was not found in the lab,
Desktop, Downloads or the keychain search. Obtaining it enables signing the
customer CSR and requesting the MDM push certificate. No private key, CSR,
issued certificate, device profile or local database is included in this change.

After certificate issuance, follow the [manual Mac enrollment runbook](../operations/mac-enrollment-testing.md).
LIVE-002 requires ACME issuance evidence and LIVE-003 requires SCEP issuance
evidence; both also require real check-ins, an APNs wake, an acknowledged inventory
query and an enabled installing-user channel. Simulator results do not satisfy
these live milestones. Device-side replacement rollback and ADE Setup Assistant
activation remain separate live tests.
