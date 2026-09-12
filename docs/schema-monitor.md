# Apple schema compatibility monitor

The **Apple Schema Compatibility Monitor** workflow checks whether this project's
generator and tested library/server behavior can handle Apple's current schemas.
It runs daily at 03:23 UTC and supports manual runs. Manual runs default to
`report_only: true`, which retains reports and proposed issues without GitHub writes.

Apple's advertised default branch supplies stable updates. It is currently
`release`; the monitor does not assume `main`, derive a year from an OS version, or
assume a particular seed naming format. Every branch starting with `seed` is assessed
even when stable has not changed. A seed advertised as the default is a discovery
failure requiring investigation.

## What a cycle does

1. Record the project commit, Apple default branch and commit, project submodule
   pin, and all seed names and commits in `discovery.json`.
2. Assess stable against the project pin, and each seed against that same stable
   snapshot. If stable does not yet contain the adopted seed, use the retained
   release baseline for a comparison-only assessment. Matrix jobs run independently
   with at most two assessments in parallel.
3. Compare raw schema structure and collect strict parsing failures across all
   files. Group repeated causes and retain independent protocol/support changes.
4. If parsing succeeds, retain the project's pinned historical schema for all
   candidates, generate the combined API, verify deterministic
   output and exported-name removals, and compare generated public declarations,
   signatures and serialization tags against the project API. Comparison-only
   stable assessments instead compare against freshly generated retained-release
   types and their identifier lock; they do not certify published API compatibility.
5. Build both Go modules; verify the server resolves the candidate library through
   `go.work`. Compare source-derived support cases with the compiled tables and run
   generated conformance, MDM protocol, server service and DDM adapter tests with
   the race detector. Changed support cases include stable and candidate OS
   boundaries and device/user, supervision, ADE, user approval, shared iPad and
   user enrollment contexts.
6. Reconcile engineering issues and publish generated changes. Adoptable stable uses one
   normal PR on `schema/update-stable`; each seed uses one draft on
   `schema/preview/<Apple branch>`. A parsing/generation failure creates issues
   without an empty or partially generated PR. Existing previews explain when
   their content represents an older candidate.

When stable predates the adopted seed, publication is not applicable: the monitor
produces no downgrade patch and closes any existing monitor-owned stable update
PR. Its report identifies the release baseline used. Such a comparison cannot
close an existing published-API or identifier-verification incident. Once stable
contains the adopted seed, normal published-API checks and update PRs resume.
The historical gitlink remains pinned through both paths.

Every report distinguishes `passed`, `failed`, `blocked` and, for publication,
`not-applicable`. An assessment can complete while reporting an incompatibility.
The workflow then succeeds at its monitoring job while recording the failure in
the report and issue. Missing evidence, discovery failures and publication errors
make the workflow fail. Blocked runtime checks are never presented as passing.

Passing tests establish those scenarios. They do not certify every Apple behavior
or replace testing on real devices. Protocol prose and new server responsibilities
require an engineer's review even when compilation and conformance tests pass.

For `seed_OS_27_0` and candidates assessed against the adopted OS 27 API, the tests
stage also enables `schema_seed_os_27` and requires
explicit passing JSON test events for enhanced-log commands and status, software
update removal, Return to Service retry, the reviewed content-cache contract,
mixed-fleet dispatch, queued commands after an upgrade, and legacy-profile wire
compatibility. All eight contracts must pass.
Missing or skipped tests fail the stage. Comparison-only older stable assessments
do not compile these types. Content-cache tests run in both assessments; OS 27 additionally
checks its OpenAPI file against the reviewed library fixture.

`make test` also runs `make test-schema-contracts`, using the same eight-test
evidence check. Its JSON test log and result are retained in `cover/schema-contracts`.

## Engineering issues

Each ticket starts with what changed and the observed project impact, followed by
a required-work checklist, completion criteria, blockers and links to relevant
code and Apple inputs. Confirmed parsing/check failures, compatibility verification
and support decisions are identified explicitly. An optional new capability is
not presented as a demonstrated runtime regression. Candidate-dependent reviews
link to parser or generation blockers by finding identity, including on the first
publication cycle.

Raw source evidence and reproduction details are collapsed. Long protocol prose
is displayed around the changed clauses, so a requirement near the end of a
paragraph remains visible. Known equivalent edits to Markdown, contractions and
requirement phrasing do not create reviews. Availability prose is suppressed only
when the affected structured availability is unchanged. Other prose changes remain
review evidence; the full comparison remains in `audit.json`.

All managed issues carry `schema-monitor` and one of the labels below. Identity is
the Apple branch plus a normalized cause; hundreds of identical metadata failures
produce one issue, with affected paths in the retained evidence.

| Kind | Label | Example and required action |
|---|---|---|
| Schema format failure | `schema-gap` | Unknown metadata, invalid YAML, unsupported generation input. Check Apple's schema definition and implement deliberate support or investigate upstream. |
| Public API failure | `schema-gap` | Exported name removal, changed Go type/signature or wire tag. Preserve compatibility or propose a reviewed migration. |
| Runtime compatibility failure | `schema-gap` | Candidate build, support boundary or existing protocol/service test fails. Reproduce using the recorded commits and add the necessary fix. |
| Behavior review | `schema-review` | Changed availability/enrollment rules, new commands, check-in/DDM protocol fields or wording. Record required server changes or why existing handling suffices. |
| Upstream input/scope | `schema-review` or `schema-gap` | New input area needs a support decision; missing/invalid referenced JSON examples need investigation. |
| Automation failure | `schema-automation` | Discovery, missing matrix results, snapshot mismatch or PR publication failed. Repair the workflow or credential and rerun. |

Issues update when evidence, project/candidate identity, status or the versioned
presentation changes. The bot refreshes managed titles and its marked evidence
block, preserving comments, engineer notes outside the block and checked tasks
whose instruction text is unchanged. Formatting changes do not reopen a
maintainer-closed issue. Repeating the same scan and presentation produces no writes
or daily comments. Migrating review evidence for an unchanged Apple snapshot also
preserves a maintainer's closure, including when editorial filtering changes the
evidence fingerprint. Reproducible failures close only when the relevant stage
actually passes in a completed later assessment. Behavior reviews require an
engineer to close them. An automatically verified failure reopens if it recurs;
an unchanged finding closed by a maintainer remains acknowledged. New evidence
can reopen that acknowledgment.

If Apple retires a seed, its open issues become **inactive**, not fixed, and its
bot-owned preview PR closes. Discovery failure cannot retire branches. A seed
that becomes identical to stable no longer needs a separate preview.

## Reproduce locally

Discovery and assessment require Git, Go (the version in `go.mod`) and Python 3.
Publication additionally uses the GitHub CLI. Commit implementation changes before
assessing: the runner clones the recorded project commit, so uncommitted edits are
not part of the tested snapshot.

```sh
python3 .github/scripts/schema_monitor.py discover --output /tmp/schema-discovery.json
```

Read a branch's `key` from the manifest, then run:

```sh
python3 .github/scripts/schema_monitor.py assess \
  --manifest /tmp/schema-discovery.json --key BRANCH_KEY --output /tmp/schema-reports
python3 .github/scripts/schema_monitor.py publish \
  --manifest /tmp/schema-discovery.json --output /tmp/schema-reports --report-only
```

Assess every manifest entry before publication. Missing entries deliberately produce
an automation finding. For an exact historical reproduction, download the original
`schema-discovery` artifact and check out its `projectCommit`; a new discovery may
select newer Apple commits. The workflow retains discovery, per-branch reports,
logs, support cases and any candidate patch for 30 days.
Report-only publication also retains one Markdown preview per proposed issue in
the `schema-monitor-summary` artifact, alongside `proposed-issues.json`.

The runner checks candidate and project SHAs before and after generation/tests.
It invokes `schemagen` directly. `make generate` and `make verify` initialize the
committed submodule pin and would reset a manually selected candidate checkout.
Preview patches update `.gitmodules` so subsequent local generation records the
correct Apple ref and initializes the pinned historical input. The history
gitlink is checked before assessment completes and again before publication.
They never update `ALLOWED_REMOVALS.md`, handwritten Go files,
server dependency requirements or release metadata.

For a raw source comparison without running candidate code:

```sh
go run ./cmd/schemagen -schema /path/to/seed -baseline /path/to/stable \
  -ref seed_OS_27_0 -report /tmp/schema-audit audit
```

## Publication and adoption

Discovery and assessment jobs have read permissions. The publisher consumes
restricted generated patches and reports; it does not run candidate code. Issues
use `GITHUB_TOKEN`. PR publication uses the configured `RP_APP_ID` and
`RP_APP_PRIVATE_KEY`, with `RELEASE_PLEASE_PAT` as fallback, so PR CI can run
automatically. Missing PR credentials produce a publication incident when there
is a patch to publish; existing assessment evidence remains available.

Fix generator and runtime findings in separate PRs, then rerun the monitor.
Do not remove strict decoding just to obtain a green seed report. Review a seed
preview before adoption; it does not automatically promote into stable. Publish
needed library changes before deliberately updating the server module dependency.

## Initial source baseline

At implementation, Apple `release` was `67045e2fa06f528b196c01edee6a8bf88b844beb`
and `seed_OS_27_0` was `b0180185a5e4077070710033341b71d0cbe1a18a`.
The original comparison contains 314 → 343 schema files: 29 additions and one
filename correction. Parser support now includes the top-level `examples` in 304
files and `ReasonDetail.valuetype` in two files. Example references remain audited;
the timestamp annotation preserves the underlying string type. Apple's recorded
meta-schema defines the example structure but omits `valuetype`, so the latter is
an explicit compatibility annotation based on the source files.

The compatibility work retains the stable submodule pin. Seed generation can
expose additional upstream API removals or type changes; runtime compatibility
tests do not authorize those changes. API and exported-name guards continue to
report them independently, and any seed adoption needs a separate migration
decision. The content-cache library supports its reviewed OpenAPI separately;
other new input areas still require an engineering support decision.
