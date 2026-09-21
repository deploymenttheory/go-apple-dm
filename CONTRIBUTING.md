# Contributing

Report bugs and propose changes through the [issue templates](https://github.com/deploymenttheory/go-apple-dm/issues/new/choose).
Follow the [code of conduct](CODE_OF_CONDUCT.md). Report vulnerabilities through the process in
[SECURITY.md](SECURITY.md).

## Development and validation

Use Go 1.27 and initialize the pinned Apple schema with `git submodule update --init`.
The workspace contains two modules: the root library and `server`. Reusable library packages
live under `devicemanagement/`; their imports include that prefix. The library must not import
the server, including in tests. The package import constraints are documented in
[architecture.md](docs/architecture.md) and enforced by `internal/layout`.

Run checks appropriate to the change. `make verify` checks generated output; `make test` runs
both modules with the race detector and requires all published OS 27 contracts through
`make test-schema-contracts`. Storage changes require the shared contract suite and
SQL integration tests. Protocol changes require relevant simulator scenarios and failure-path
tests. `make help` describes the database, end-to-end, fuzz and coverage targets. The coverage
floor is 95% overall and per non-exempt package; exemptions are listed in
[scripts/coverage-exempt.txt](scripts/coverage-exempt.txt).

`make lint` checks the complete baseline in both workspace modules without changing
files. `make tools` installs the version in `.golangci-version` with the declared
Go toolchain; `make fmt` explicitly formats authored Go files. The shared lint
runner selects the committed workspace even if the shell has `GOWORK=off`,
validates configuration, and compiles tests before analysis. Integration, E2E,
acceptance and OS 27 schema test tags are included without running those tests or
contacting their services. Reports are written beneath `cover/lint/`.

The blocking rules cover correctness, security, error handling, HTTP/SQL resource
ownership, serialization tags, logging, compiler directives and test helpers.
Staticcheck runs its `SA*` checks. `gofumpt` and `goimports` provide formatting;
project imports use the full repository module prefix. Complexity thresholds,
allocation suggestions, tag-casing mandates and mandatory error wrapping are not
part of the policy. Context propagation through request/exchange objects is
reviewed with lifecycle tests; `contextcheck` cannot reliably follow those paths.
Optional `(nil, nil)` contracts and compile-only examples are permitted.

Authored tests, examples and tools receive the same analysis as production code.
Strict generated-file markers exclude diagnostic/formatting reports, not
compilation. Never hand-edit generated sources to fix a finding. Preserve Apple's
wire keys and existing stored JSON names when adding explicit tags. Existing
module and tier constraints remain enforced by `internal/layout`.

Fix findings at their cause. A deliberate best-effort cleanup or reporting error
may be explicitly discarded, but setup, persistence and validation errors must be
handled. Test response bodies and database cursors still have owners; explicit
early SQL closure must not become an accumulating defer inside a loop. Narrow
`//nolint:linter // reason` directives require the named check, a concrete reason
and an actual finding. Security exceptions use `#nosec Gxxx -- reason` for the
specific protocol requirement or controlled fixture. Do not exclude a whole test
file or disable security analysis merely because it contains a negative fixture.
Remove obsolete suppressions and keep useful protocol explanations as comments.
`make verify-server-module-installation` verifies that the server can be used
outside this repository with the root library version declared in `server/go.mod`.
It packages the current server sources in a temporary module proxy, disables Go
workspaces, rejects replacement directives, and checks that dependency resolution
selects exactly the declared root library version. It builds a separate application
importing public server packages, builds all server packages, and runs `go install`
for `dmserver` and `dmctl` into a temporary directory. The root library and other
dependencies resolve through the configured Go proxy with public checksum
verification; the locally packaged candidate server is exempt from checksum lookup.
The output reports these four stages and the versions used. This is a build and
installation compatibility check; runtime behavior has separate test suites.
Publish library API additions before raising the server requirement to the version
containing them. To verify an already published server module, run:

```sh
python3 scripts/verify-server-module-installation.py --server-version vX.Y.Z
```

## Documentation and design

Describe current behavior in direct, neutral English. Explain contracts, operational requirements,
limitations and useful design rationale. Distinguish Apple protocol requirements from project
policy. Use Apple's enrollment terminology; preserve exact Go identifiers and wire keys.
Avoid development milestones, competitive claims and comments that repeat the code.

Use a package comment in `doc.go`: a `Package` summary, relevant behavior and constraints,
and useful references. Add `# Design` or other sections when they improve navigation. Function
comments describe inputs, results, errors and significant side effects or concurrency requirements.
Do not paraphrase verbatim Apple schema descriptions.

Document significant design decisions using the [decision template](docs/research/decisions/TEMPLATE.md).
Integrate amendments into the current decision and preserve its number and filename. Update
[architecture.md](docs/architecture.md), related guides and diagram sources when behavior changes.
Run `make docs-check` for local documentation links, code references, documentation
contracts and diagram-source checks; this check does not modify files. Reconcile
protocol claims with primary vendor sources and link them beside the relevant contract.
Keep authored documentation about current behavior: consolidate duplicate guides and
remove superseded reports, handoffs and migration narratives. Generated release
changelogs, schema provenance and vendor snapshots retain their own ownership.
Diagram JSON sources and regeneration instructions are in [docs/diagrams](docs/diagrams/README.md).

Edit project-authored generated comments in `internal/schemagen`, then run `make generate` and
`make verify`. Do not hand-edit generated `doc.go` files, `*.gen.go`, `devicemanagement/schema/EXPORTED_IDENTIFIERS.lock` or
`devicemanagement/schema/GENERATED_FROM.json`. Record intentional exported-name removals in
[schema/ALLOWED_REMOVALS.md](devicemanagement/schema/ALLOWED_REMOVALS.md).

The [Device Management Client Schema monitor](docs/schema-monitor.md) checks upcoming
Apple schemas against the generator on main, using the published pin as a control.
It reports code generation failures with source evidence and remediation guidance.
Use report-only mode to retain proposed incidents without publishing. Ordinary CI
runs published feature contracts separately from the monitor.

## Pull requests

Use Conventional Commit titles. Explain the problem, resulting behavior, relevant tradeoffs
and validation. Link related issues and design decisions. Keep dependencies justified and
include migration requirements for persistent or protocol state. Release-please manages
versions and changelogs; ordinary documentation changes do not change release metadata.

The repository-owned `release-please--branches--main` PR prepares release metadata
and skips test, security, dependency-review and title-validation jobs. Go Test
and Security skip ordinary PRs and pushes to `main` when every changed path
matches `docs/**`, `**/*.md` or `.release-please-manifest.json`; workflow changes
run these checks. Dependency Review additionally excludes workflow-only changes.
These documentation exclusions include root and server changelogs. Mixed changes
that include application code or dependency files retain normal CI.
Go lint runs for Go sources, module/workspace files, lint configuration/tooling or
workflow changes, and supports manual dispatch. It checks both workspace modules
on native Linux ARM and Windows without modifying files. Compilation, analysis
and standalone installation are separate checks; a typechecking failure is not
a successful zero-finding run.
Server tags use `server/vX.Y.Z`. The **go | Published server module installation**
workflow downloads that published server version and verifies dependency resolution,
package builds and command installation. It also accepts a version through manual
dispatch. This workflow reports installation failures after publication; it does
not block tag creation. The Go Test workflow runs the same verification on candidate
server sources before merge. Release-please owns normal tags and changelogs. A published Go
pseudo-version can pin an independently reviewed library commit before its next tag.
PR title validation remains enabled for ordinary documentation and workflow PRs.
Validate workflow edits locally with `actionlint` and `make verify` before submitting them.
The [CI responsibility matrix](docs/testing/ci.md) records which checks cover source,
modules, backends and release archives. Release archive previews exclude server Markdown
and manifest-only updates but retain changes to packaging code and its operational guide.
Every feature change must update the affected current documentation, examples and diagrams.
Record validation results and their source revision, device scope and limitations in the PR;
keep reproducible procedures in the maintained guides.
Release-please still runs on `main` pushes to manage releases and tags, and the
scheduled security scan remains enabled.
