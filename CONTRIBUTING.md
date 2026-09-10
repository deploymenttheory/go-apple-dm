# Contributing

Report bugs and propose changes through the [issue templates](https://github.com/deploymenttheory/go-apple-dm/issues/new/choose).
Follow the [code of conduct](CODE_OF_CONDUCT.md). Report vulnerabilities through the process in
[SECURITY.md](SECURITY.md).

## Development and validation

Use Go 1.27 and initialize the pinned Apple schema with `git submodule update --init`.
The workspace contains two modules: the root library and `server`. The library must not import
the server, including in tests. The package import constraints are documented in
[architecture.md](docs/architecture.md) and enforced by `internal/layout`.

Run checks appropriate to the change. `make verify` checks generated output; `make test` runs
both modules with the race detector. Storage changes require the shared contract suite and
SQL integration tests. Protocol changes require relevant simulator scenarios and failure-path
tests. `make help` describes the database, end-to-end, fuzz and coverage targets. The coverage
floor is 95% overall and per non-exempt package; exemptions are listed in
[scripts/coverage-exempt.txt](scripts/coverage-exempt.txt).

For a review that must preserve formatting, run golangci-lint with `--fix=false` in each module.
The checked-in configuration otherwise enables automatic fixes.

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
Diagram JSON sources and regeneration instructions are in [docs/diagrams](docs/diagrams/README.md).

Edit project-authored generated comments in `internal/schemagen`, then run `make generate` and
`make verify`. Do not hand-edit `*.gen.go`, `schema/EXPORTED_IDENTIFIERS.lock` or
`schema/GENERATED_FROM.json`. Record intentional exported-name removals in
[schema/ALLOWED_REMOVALS.md](schema/ALLOWED_REMOVALS.md).

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
Go lint runs for Go sources, module files, `.golangci.yml` or workflow changes,
and supports manual dispatch. It checks both modules without modifying files.
PR title validation remains enabled for ordinary documentation and workflow PRs.
Validate workflow edits locally with `actionlint` before submitting them.
Release-please still runs on `main` pushes to manage releases and tags, and the
scheduled security scan remains enabled.
