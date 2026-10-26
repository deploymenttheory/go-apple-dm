# 0048: Reference server as the maintained acceptance lab

## Context

Protocol fixtures can validate individual services without exercising the shipped
server's startup, identity setup, profile generation and observation. Acceptance
must also exercise that composition and expose reproducible operator workflows.
Acceptance additionally has to cover real devices across several Apple OS releases,
virtual and physical, without a second harness, a second result format or a second
catalogue.

## Decision

`dmserver` owns the serving implementation through `server/internal/runtime` and
`app.Build`. Its native TLS option complements existing reverse-proxy deployment.
`dmctl lab` owns private workspaces, process supervision, external fixtures and a
Go module catalogue. It is the single acceptance entry point: there is no separate
bench command, catalogue or report.

The harness is a public package (`server/lab`, with the device contract in
`server/lab/target`) rather than an internal one, because device acceptance needs
material this repository must not carry: Apple credentials, virtual-machine images,
per-release interface tables and run evidence. A separate private lab registers its own
modules and drivers against these types and reuses this runner, result model and report,
so one catalogue shape and one evidence format serve both.
`lab.Connect` attaches this private consumer to an existing managed deployment
using its exported trust and managed administrator credential. It verifies the
principal without creating another workspace or certificate store. `lab.SelectFrom`
selects an extended catalogue, and `lab.Command` exposes the tracked command and
APNs acknowledgment primitive with optional run-scoped evidence.

Two dimensions vary independently.

- **Modes** select the service environment a module needs. A `simulated` workspace
  provides local APNs, DEP, ABM, OIDC, attestation and admission fixtures. A `live`
  workspace uses the operator's Apple credentials and a real device.
- **Targets** (`server/lab/target`) supply the device side: the protocol
  simulator, or a device reached through a driver. A target reports its platform,
  OS, hardware and enrollment identifiers, and the operations it can perform.
  Modules declare the capabilities they need and the conditions they apply under,
  so an inapplicable module is reported as unsupported with a reason rather than
  failing.

A module is one maintained check with an ordered list of steps, a theme and a
lifecycle stage. Stages order a run for one target, from preflight and enrollment
through inventory, configuration, apps and unenrollment. A module marked as a gate
blocks later modules for that target when it does not pass, which is how an
unavailable APNs path on a device stops the lab from reporting downstream results
it cannot have observed. Modules tagged destructive run only when explicitly
enabled.

Every run writes the same evidence: `results.json`, `junit.xml` and a
self-contained `report.html`. Results distinguish passed, failed, blocked and
unsupported, and record the target, stage, steps and evidence files.

The supervisor's authenticated loopback control endpoint is separate from the
server. No fixture control, test clock or fault-injection route is mounted in the
normal application. Module-specific configuration starts isolated instances.
Local workspaces persist; automated acceptance creates disposable workspaces.

Reusable operator capabilities belong in the reference server: service discovery,
enrollment-profile issuance and replacement, single-use enrollment links
([0058](0058-enrollment-links.md)), issuance evidence, enrollment-scoped
command-result retrieval, configured OTA and user identity verification, and app
notification administration. The native host app is a device-side fixture.

Contract suites continue to exercise interfaces directly. Detailed E2E regressions
retain timing, storage and fault assertions that need component access; they are
component regression tests, not an acceptance layer. Their IDs are linked to the
shared catalogue; process acceptance does not claim to execute those internal
assertions. The Makefile exposes both layers and the lab commands.

LIVE-010 uses a separate ADE server token for read-only authentication, account
binding, full/delta enumeration and designated profile readback. LIVE-011 adds
explicit profile definition/assignment and restoration through the reference
worker. An exclusive private fence captures original state and its
progress-directory locator before mutation; unanswered requests retain uncertainty
and cannot trigger automatic inverse replay or a later run.

Per-feature generated support governs DDM, including supported tvOS status
subscriptions; native DDM exchange and cleanup remain independent. Each requested
platform has its own passed/failed/unexercised row.

## Rationale

A shared runtime catches configuration and integration failures that standalone
service fixtures cannot detect. Keeping module steps independent of `testing.T`
allows operators and automated tests to reuse them without embedding the Go test
runner into production tooling. Existing Go fixtures remain reusable; a separate
workflow language or production test-control API is unnecessary.

Separating modes from targets keeps one catalogue usable by both the simulator in
CI and a real Mac on a desk: the module states what it needs, and the runner
decides what that means for the selected workspace and device. Folding the device
into the mode would have forced either duplicate modules per driver or a second
harness for real devices.

Combining every test into subprocess acceptance would sacrifice deterministic
clocks and useful interface assertions. Maintaining a second lab daemon would
duplicate runtime composition. The chosen boundaries share implementation and
workflows while preserving each test layer's observation points.

## Constraints

A simulated pass is not proof of Apple interoperability. Missing live prerequisites
produce blocked results; unsupported modes and targets are explicit. Default
execution is simulated. Live return-to-service and other destructive or
device-specific modules are not implicitly enabled.

Reports contain module outcomes, not credential dumps. Native TLS certificates,
enrollment issuers and storage keys must persist across restarts. Init preserves
complete existing local identities and refuses partial sets. Live storage retains
its configured database and storage-key names, including workspaces written before
the lab replaced the bench: `Load` reads the current `lab.json` and the earlier
`bench.json`, and the retained storage-key name is unchanged. Configuration changes
require stopping the workspace first.

A dedicated quiescent account is required for ADE assignment acceptance because
the target API has no compare-and-swap. ADE membership cannot replace missing
native facts. Fixture and API passes cannot certify physical enrollment. The
[ADE guide](../../operations/ade-acceptance.md) defines private prerequisites and
operator steps.

## Verification

`make test-e2e` runs the shared modules against the embedded runtime together with
the retained detailed regressions. `make test-acceptance` runs the same modules
against built `dmserver` processes with unified device management.
`make test-contract` verifies the underlying persistence interfaces.
`make lab-docs-check` compares the generated catalogue documentation with code.

Enrollment modules use the same profile API for ACME and SCEP and exercise
successful and failed replacement. Offline preflight and trust export prepare a
live Mac through `lab-preflight`, `lab-trust`, `lab-profile` and `lab-replace`.
Single-use enrollment links let a device fetch its own profile from a browser.
Live acceptance requires recorded issuance, check-in, an APNs-triggered inventory
response and the installing user's management channel. These modules require actual
device evidence before they can pass. The lab derives a stable ACME identifier key
from its retained workspace secret so a restart does not invalidate previously
issued identifiers.

Unit tests run the maintained modules against the embedded reference server with
interrupted exchanges and incomplete evidence. They also exercise missing workspace
prerequisites, supervisor exits, and live receipt and acknowledgement validation
through local fixtures. A negative enrollment module must observe an explicit
protocol rejection; a transport error or unavailable service is a failure.
Coverage retains the repository's 95% overall and per-package gates, with no
additional exemptions for the lab.

Apple Business acceptance uses `LIVE-007` for populated collection and exact
export, `LIVE-008` for explicit authenticated serial correlation, and `LIVE-009`
for designated assignment, per-device log/readback and original-assignment
restoration. A private workspace file supplies the account and resource scope;
mutation requires separate explicit opt-in. Missing or empty prerequisites are
blocked/unexercised; authorization failures are failures. Cleanup has its own
bounded context and retains unresolved restoration instead of reporting success.
Fixture execution follows the actual runtime and SDK workers, and cannot prove
live Apple or hardware compatibility.

LIVE-012 requires stored ADE issuance, completed authenticated check-ins, pinned
identity continuity and fresh APNs-triggered native serial/platform/OS/build and
typed supervision evidence.

LIVE-013 performs dedicated Apps and Books sToken authentication and read-only
location collection without claiming ownership or changing callbacks. LIVE-014
requires explicit free-app and quiescent-location attestations, captures original
assignments and revision before mutation, and proves SDK/reference completion,
authenticated callback, per-target readback and revocable-license restoration.
Uncertainty retains an immutable private fence across output-directory changes;
any unsuccessful outcome after the fence is a failure, never blocked.
LIVE-015 inspects an operator-selected prepared managed-content operation and
fresh native installed/version facts without changing apps or licenses. It
records each platform independently and does not imply external setup cleanup.
See the [Apps and Books guide](../../operations/apps-and-books-acceptance.md).

## References

- [Testing guide](../../testing/lab.md)
- [Lab runbook](../../../test-lab/README.md)
- [Local lab acceptance testing](../local_lab_acceptance_testing.md)
- [Single-use enrollment links](0058-enrollment-links.md)
- [Server-managed app pushes](0049-server-managed-app-push.md)
- [Admin API](0034-admin-api-and-authorization.md)
- [CLI conventions](0035-dmctl-structure-and-credentials.md)
- [Enrollment profiles](0009-enrollment-profiles.md)
- [ADE enrollment and service configuration](0027-ade-enrollment-machineinfo-and-web-view-auth.md)
- [Mac enrollment runbook](../../operations/mac-enrollment-testing.md)
