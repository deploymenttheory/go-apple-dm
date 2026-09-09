# 0048: Reference server as a maintained test bench

## Context

The local APNs spike added a `dmlab` executable and Python orchestration while
existing E2E fixtures separately composed protocol services. This duplicated
startup, identity setup, profile generation and observation. Passing those tests
could leave the shipped server's wiring unexercised.

## Decision

`dmserver` owns the serving implementation through `server/internal/runtime` and
`app.Build`. Its native TLS option complements existing reverse-proxy deployment.
`dmctl bench` owns private workspaces, process supervision, external fixtures and
a Go scenario catalogue. Shared scenarios run against either an embedded runtime
or built server processes using normal administration and device APIs. Live
adapters use explicitly configured Apple credentials and device prerequisites.

The supervisor's authenticated loopback control endpoint is separate from the
server. No fixture control, test clock or fault-injection route is mounted in the
normal application. Scenario-specific configuration starts isolated instances.
Local workspaces persist; automated acceptance creates disposable workspaces.

Reusable operator capabilities belong in the reference server: enrollment-profile
issuance, enrollment-scoped command-result retrieval, configured OTA and user
identity verification, and app notification administration. The old `dmlab` and
Python runner are removed; the native host app remains a device-side fixture.

Contract suites continue to exercise interfaces directly. Detailed E2E regressions
retain timing, storage and fault assertions that need component access. Their IDs
are linked to the shared catalogue; process acceptance does not claim to execute
those internal assertions. The Makefile exposes both layers and the bench commands.

## Rationale

A shared runtime catches configuration and integration failures that standalone
service fixtures cannot detect. Keeping scenario functions independent of
`testing.T` allows operators and automated tests to reuse them without embedding
the Go test runner into production tooling. Existing Go fixtures remain reusable;
a separate workflow language or production test-control API is unnecessary.

Combining every test into subprocess acceptance would sacrifice deterministic
clocks and useful interface assertions. Maintaining a second lab daemon would
retain the original divergence. The chosen boundaries share implementation and
workflows while preserving each test layer's observation points.

## Constraints

A simulated pass is not proof of Apple interoperability. Missing live prerequisites
produce blocked results; unsupported modes are explicit. Default execution is
simulated. Live return-to-service and other destructive/device-specific scenarios
are not implicitly enabled. Reports contain scenario outcomes, not credential dumps.

Native TLS certificates, enrollment issuers and storage keys must persist across
restarts. Init preserves complete existing local identities and refuses partial
sets. Live storage retains the former database path and accepts its storage-key
name. Configuration changes require stopping the workspace first.

## Verification

`make test-e2e` runs shared embedded-runtime scenarios and retained detailed
regressions. `make test-acceptance` runs built server processes, including both
split roles. `make test-contract` verifies the underlying persistence interfaces.
`make bench-docs-check` compares generated catalogue documentation with code.

Unit tests run the maintained scenarios against the embedded reference server
with interrupted exchanges and incomplete evidence. They also exercise missing
workspace prerequisites, supervisor exits, and live receipt/acknowledgement
validation through local fixtures. A negative enrollment scenario must observe
an explicit protocol rejection; a transport error or unavailable service is a
failed scenario. Coverage retains the repository's 95% overall and per-package
gates, with no additional exemptions for the bench.


## References

- [Testing guide](../../testing/bench.md)
- [Bench runbook](../../../test-lab/README.md)
- [Server-managed app pushes](0049-server-managed-app-push.md)
- [Admin API](0034-admin-api-and-authorization.md)
- [CLI conventions](0035-dmctl-structure-and-credentials.md)
