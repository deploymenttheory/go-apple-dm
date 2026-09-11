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

`make lint` checks the complete baseline in both modules without changing files.
Use `golangci-lint fmt` explicitly when formatting changes are intended.
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
Server tags use `server/vX.Y.Z`. The **go | Published server module installation**
workflow downloads that published server version and verifies dependency resolution,
package builds and command installation. It also accepts a version through manual
dispatch. This workflow reports installation failures after publication; it does
not block tag creation. The Go Test workflow runs the same verification on candidate
server sources before merge. Release-please owns normal tags and changelogs. A published Go
pseudo-version can pin an independently reviewed library commit before its next tag.
PR title validation remains enabled for ordinary documentation and workflow PRs.
Validate workflow edits locally with `actionlint` before submitting them.
Release-please still runs on `main` pushes to manage releases and tags, and the
scheduled security scan remains enabled.
