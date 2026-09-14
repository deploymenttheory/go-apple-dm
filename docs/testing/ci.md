# CI responsibilities and maintenance

This matrix describes the workflows in the repository. Similar test names do not
necessarily mean duplicate coverage: candidate source, a published module and a
packaged executable are different inputs.

| Workflow or job | Trigger and input | Contract checked |
|---|---|---|
| Go Test: unit | Application PRs and main pushes; Linux, macOS, Windows | Both modules, races, generated conformance and published OS 27 contract evidence. Linux contributes unit coverage. |
| Go Test: candidate module installation | Same application changes; Linux and Windows | A temporary module proxy serves candidate server sources; `GOWORK=off` resolves declared dependencies, builds packages and installs commands without repairing requirements. |
| Go Test: generate check | Same application changes | `make verify` checks workflow/script contracts and regenerated output, including changed, missing and stale generated files and removed locked exported names. Verification does not rewrite generated output. |
| Go Test: storage integration | Same application changes; SQL services | Shared storage contracts on SQLite, PostgreSQL and MySQL; PostgreSQL timing is reported with its shared-runner threshold disabled. |
| Go Test: E2E | Same application changes; SQLite and PostgreSQL | Backend-specific server/device exchanges and split DDM transport. The SQLite-only embedded acceptance catalogue runs once, in the SQLite job. |
| Go Test: process acceptance | Same application changes | Shared scenarios against built `dmserver` processes, including split topology; executable bench catalogue matches its documentation. |
| Go Test: fuzz smoke | Same application changes | Brief execution of each fuzz target. |
| Go Test: coverage | Successful unit, storage and E2E jobs | Merge Linux unit, SQL contract and both E2E profiles; retain the 95% package and overall gate. Process acceptance is separate evidence. |
| Go Linter | Go/module files, linter settings or workflow PR changes; manual dispatch | Both modules on Linux ARM and Windows, including platform-specific code. Does not rewrite source. |
| Security | Application PR/main changes and weekly schedule | Both modules' vulnerability checks and gosec SARIF, plus the Docker build-context exclusion check. |
| Dependency Review | PRs other than docs/metadata/workflow-only changes | Dependency diff against the base revision. |
| Check server release assets | Server implementation/dependencies, packaging/workflow inputs, LICENSE or release operations guide | Build all six archives, verify their contents and Linux versions; execute the packaged Windows binaries and native workspace-lock test. Does not publish. |
| Published server module installation | `server/v*` tag push or explicit version dispatch | Retrieve the actual published module and verify requirements, builds and command installation with `GOWORK=off`. This reports after publication; it cannot prevent tag creation. |
| Release server | Published server release or tag-specific dispatch | Full native Windows suite gates versioned archive construction, verification, signing and upload to that existing release. |
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

The generated-output job invokes `make verify` once. Running `make generate`,
`git diff`, then `make verify` regenerated the same tree twice. Verification now
also rejects unexpected `.gen.go` and `conformance_gen_test.go` files under the
configured generated-output root, so stale output is covered without rewriting it.

The embedded `server/acceptance` E2E suite configures SQLite itself. Running that
same suite under `E2E_STORE=postgres` did not add PostgreSQL coverage; it is now
selected only for SQLite. `server/e2e` still runs against both stores, and process
acceptance still covers binary startup, supervision and split topology.

Keep candidate installation, published installation, archive smoke tests and the
release Windows suite: each checks a different artifact or publication boundary.
Keep linter gosec and scheduled gosec SARIF: their platforms, exclusions, reporting
and schedule differ. Consolidating either would require preserving those contracts.

## Dependency and failure handling

Use Go's normal dependency resolution and the existing `actions/setup-go` cache.
There is no repository-specific dependency downloader or global retry layer. The
14 September Windows candidate-install and PostgreSQL E2E setup failures were
Go proxy HTTP/2 `INTERNAL_ERROR` responses, not test assertions. A failed-job rerun
can resolve a transient proxy outage. It must not disable checksum verification,
change the required module version or hide a reproducible build/test failure.

The supervisor failure was different: `/stop` could close its connection before
its HTTP response completed. Shutdown now drains the control server before closing
the runtimes and joins its serving goroutine. The restart test probes readiness,
retains a bounded startup deadline and cancels/joins on every failure path before
its temporary workspace is removed. Increasing a timeout alone would not fix the
held Windows lock.

For workflow edits run `actionlint`, `make verify` and the affected Go tests. For
supervisor changes include repeated race-enabled restart tests and native Windows
CI. For backend selection changes require SQL contracts and both E2E jobs. Keep
release validation nonpublishing during maintenance; do not replace existing tags
or assets merely to test a fix.

Sources: [Go Test](../../.github/workflows/go-test.yml),
[Makefile](../../Makefile), [release previews](../../.github/workflows/release-check.yml),
[release workflow](../../.github/workflows/release.yml),
[published-module verification](../../.github/workflows/go-server-module-installation.yml),
[lint](../../.github/workflows/go-lint.yml), [security](../../.github/workflows/security.yml)
and [scope review evidence](../research/extension-proposals-2026-09-14.md#phase-2--pipeline-and-test-repairs-maintenance).
