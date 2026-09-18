# CI responsibilities and maintenance

This matrix describes the workflows in the repository. Similar test names do not
necessarily mean duplicate coverage: candidate source, a published module and a
packaged executable are different inputs.

| Workflow or job | Trigger and input | Contract checked |
|---|---|---|
| Go Test: unit | Application PRs and main pushes; Linux, macOS, Windows | Both modules, races, generated conformance and published OS 27 contract evidence. Linux contributes unit coverage. |
| Go Test: candidate module installation | Same application changes; Linux and Windows | A temporary module proxy serves candidate server sources; `GOWORK=off` resolves declared dependencies, builds and installs commands without repairing requirements. Binary metadata verifies the library version; installed commands run simulated process acceptance, including binary-policy validation and restart. |
| Go Test: generate check | Same application changes | `make verify` checks workflow/script contracts and regenerated output, including changed, missing and stale generated files and removed locked exported names. Verification does not rewrite generated output. |
| Go Test: storage integration | Same application changes; SQL services | Shared storage contracts on SQLite, PostgreSQL and MySQL; PostgreSQL timing is reported with its shared-runner threshold disabled. |
| Go Test: E2E | Same application changes; SQLite and PostgreSQL | Backend-specific server/device exchanges and split DDM transport. The SQLite-only embedded acceptance catalogue runs once, in the SQLite job. |
| Go Test: process acceptance | Same application changes | Shared scenarios against built `dmserver` processes, including split topology; executable bench catalogue matches its documentation. |
| Go Test: fuzz smoke | Same application changes | Brief execution of each fuzz target. |
| Go Test: coverage | Successful unit, storage and E2E jobs | Merge Linux unit, SQL contract and both E2E profiles; retain the 95% package and overall gate. Process acceptance is separate evidence. |
| Onboarding quickstart | Onboarding docs, Compose/helper scripts, Docker/build inputs and Go/module changes; manual dispatch | Offline documentation examples, JSON/local links, bootstrap failure/resume contracts and isolated Compose HTTPS/admin-handoff/restart checks. No Apple services or physical enrollment. |
| Go Linter | Go/module/workspace files, lint configuration/tooling or workflow PR changes; manual dispatch | Both workspace modules on Linux ARM and Windows. Compiles tagged tests before full-baseline analysis; retains module/platform reports and does not rewrite source. |
| Security | Application PR/main changes and weekly schedule | Both modules' vulnerability checks and gosec SARIF, plus the Docker build-context exclusion check. |
| Dependency Review | PRs other than docs/metadata/workflow-only changes | Dependency diff against the base revision. |
| Check server release assets | Server implementation/dependencies, packaging/workflow inputs, LICENSE or release operations guide | Build all six archives, check hashes and Linux versions; execute the packaged Windows binaries and native workspace-lock test. Does not publish. |
| Published server module installation | `server/v*` tag push or explicit version dispatch | Retrieve the actual published module and verify requirements, builds, installation and process acceptance with `GOWORK=off`. This reports after publication; it cannot prevent tag creation. |
| Release server | Release Please server output or tag-specific dispatch | Build the tagged sources, check hashes and Linux executable versions, sign checksums and upload assets to the existing release. |
| Apple Schema Compatibility Monitor | Daily schedule or manual dispatch | Discover immutable upstream revisions, assess changes and retain evidence; report-only dispatch avoids publishing. Distinct from checking generated files at the current pin. |
| PR title / Release Please | Ordinary PR title changes / main pushes | Conventional Commit titles / managed release metadata and tags. |

## Path selection and duplication

Go Test and Security skip a change only when all paths match `docs/**`, `**/*.md`
or `.release-please-manifest.json`. A mixed documentation/code change still runs.
Dependency Review additionally excludes workflow-only changes. Go Linter uses its
own positive path list. The repository-owned Release Please PR has additional job
conditions because it prepares metadata; those are separate from path selection.

Release archive previews exclude `server/**/*.md` and manifest-only changes.
`docs/operations/server-releases.md` remains an explicit trigger because it contains
the packaging/verification procedure. Root library changes alone do not change the
standalone server archive's declared dependency; a change to `server/go.mod` does.

The generated-output job invokes `make verify` once. It compares expected output
without rewriting the tree and rejects unexpected `.gen.go` and
`conformance_gen_test.go` files under the configured generated-output root.

The embedded `server/acceptance` suite configures SQLite itself and runs only in
the SQLite E2E job. `server/e2e` runs against both stores; process acceptance
covers binary startup, supervision and split topology with local fixtures.

Candidate installation, published installation and archive smoke tests check
different inputs. Native Windows unit tests run in application CI; release
publication builds and checks the binaries without repeating that full suite.
Keep linter gosec and scheduled gosec SARIF: their platforms, exclusions, reporting
and schedule differ. Consolidating either would require preserving those contracts.

The linter version is pinned in `.golangci-version` and shared by local installation
and CI. `scripts/lint.py` selects this checkout's workspace explicitly; server
tests can therefore exercise library APIs introduced in the same change. The
independent candidate/published installation checks retain `GOWORK=off` and the
server's declared library dependency. Their acceptance driver runs the installed
executables in disposable SQLite/TLS workspaces, including separate server roles
and CLI lifecycle. It does not contact Apple or enroll a physical device. Lint
cannot replace those consumer checks.
A compilation error stops analysis even if a tool prints "0 issues". Investigate
the selected module version and build tags before changing exclusions.

## Dependency and failure handling

Use Go's normal dependency resolution and the existing `actions/setup-go` cache.
There is no repository-specific dependency downloader or global retry layer.
Distinguish proxy/network setup failures from assertions. A failed-job rerun can
resolve a transient Go proxy outage; preserve checksum verification and required
module versions, and investigate reproducible build or test failures.

Supervisor shutdown must drain the control HTTP response before closing runtimes
and join its serving goroutine. Restart tests probe readiness with a bounded
startup deadline and cancel/join every failure path before removing workspaces.
A held Windows lock requires lifecycle investigation, not only a longer timeout.

Embedded runtimes receive the original bound listener through `ServeListener`;
startup failure releases unclaimed listeners. Process adapters bind their own
sockets and report failures. Do not hide failed exchanges with port changes or
scenario retries. Negative protocol tests require the expected rejection, not an
unrelated transport error.

Use escaped base64 paths for OCSP GET as specified by
[RFC 6960 Appendix A.1](https://www.rfc-editor.org/rfc/rfc6960#appendix-A.1).
Use fake clocks for consistency-lag and backoff boundaries so scheduling delays
cannot substitute for the intended state transition.

For workflow edits run `actionlint`, `make verify` and the affected Go tests. For
supervisor changes include repeated race-enabled restart tests and native Windows
CI. For backend selection changes require SQL contracts and both E2E jobs. Keep
release validation nonpublishing during maintenance; do not replace existing tags
or assets merely to test a fix.

Sources: [Go Test](../../.github/workflows/go-test.yml),
[Makefile](../../Makefile), [release previews](../../.github/workflows/release-check.yml),
[release workflow](../../.github/workflows/release.yml),
[published-module verification](../../.github/workflows/go-server-module-installation.yml),
[lint](../../.github/workflows/go-lint.yml) and [security](../../.github/workflows/security.yml).

The [onboarding workflow](../../.github/workflows/onboarding.yml) runs
`make test-quickstart` independently of the documentation exclusions above.
Its smoke test uses a unique Compose project and ephemeral host port, and removes
only that test project's volume. Failures in local startup or persistence block
the check; simulator success does not substitute for physical-device evidence.
