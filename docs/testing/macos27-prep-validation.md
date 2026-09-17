# macOS 27 preparation validation

Recorded 16 September 2026 on macOS **26.6.2 (25G83)**, arm64, Go **1.27.1**.
Checkout base: `f84a831509fbc9d88f7810866a0b49fbe97b2d3e`, with the prepared working-tree
changes. This record concerns local preparation; physical macOS 27 acceptance is
pending. Start the next session with the [handoff](macos27-handoff.md).

The subsequent upgraded-device run is recorded separately in
[physical macOS 27 validation](macos27-live-validation.md). The preparation
results and earlier macOS 26 evidence below retain their original scope.

| Check | Result | Private evidence |
|---|---|---|
| Both modules, unit tests with race detector and OS 27 tag | Passed | `test-lab/local/apple27/evidence/unit-race.log` |
| OS 27 required contracts (12, none missing/skipped) | Passed | `test-lab/local/apple27/evidence/schema-contracts/` |
| Generator tests, including reused-type and array asset paths | Passed | `test-lab/local/apple27/evidence/final-ddm-generator.log` |
| Generated-output and exported API verification | Passed | `test-lab/local/apple27/evidence/generate-verify.log` |
| Lint, both modules with integration/e2e/acceptance/OS27 tags | Passed | `test-lab/local/apple27/evidence/lint.log` |
| Content-cache/shared-state and app integration on PostgreSQL/MySQL | Passed | `test-lab/local/apple27/evidence/sql.log` |
| SQLite and memory persistence, restart, retention, paging, rotate/revoke | Passed | Unit/app and SQL logs |
| End-to-end simulator and embedded acceptance suites | Passed | `test-lab/local/apple27/evidence/e2e.log` |
| Schema monitor Python contracts (51) | Passed | `test-lab/local/apple27/evidence/python.log` |
| Offline bundle helper (3 tests) | Passed | `test-lab/local/apple27/evidence/fixtures.log` |
| Local `dmserver` and `dmctl` binaries | Built | `test-lab/local/bin/` |
| Physical macOS 26 MDM acceptance | Not repeated during this preparation pass; earlier physical results are retained below | `before-host-final.json` records host facts only |
| Physical macOS 27 acceptance | Pending user upgrade/reboot | Follow handoff |
| Standalone server module using its currently published dependency | **Blocked on coordinated library publication/version bump** | `test-lab/local/apple27/evidence/standalone.log` |

The full unit/race and simulator suites ran before the final dependency-array
review. The affected DDM/generator suites, twelve contracts, CLI/profile tests,
lint and builds were run again after the relevant edits. Database storage behavior
was unchanged by that review. Evidence filenames distinguish the broad run from
final targeted checks; no result here claims the host has already run macOS 27.

## Earlier physical macOS 26 evidence

The preparation check inspected the default `test-lab/local/bench.json` path and
missed the retained workspace at `test-lab/local/certs/bench`. Its absence at the
default path does not mean physical macOS 26 testing was never performed.

On macOS 26.6.2 (25G83), private evidence records successful inventory, SCEP and
installing-user acceptance (`LIVE-003`), DDM application/removal (`LIVE-004`),
issuer/HTTPS rollover and encrypted SQLite recovery. See
`test-lab/local/certs/candidates/issuer-https-rollover/accepted-rollover-recovery.json`
for the 13 September reports and their candidate source/binary fingerprints.
Selected-feature and encrypted FileVault recovery checks, including restart,
are retained under `test-lab/local/certs/candidates/selected-features/`.

These are completed physical results for their recorded code and scenarios. They
do not establish that the later prepared working tree, every OS 27 fixture, or
all macOS 26 behavior was physically retested. Preserve their original limits
alongside the automated mixed-version regression evidence.

New cache-store coverage is 95.3%, prose-validation coverage 98.2%, and shared
profile-inspector coverage 95.1% in their direct package runs. These are not a claim
that the repository-wide merged coverage gate ran. Its normal CI job combines
unit, integration and end-to-end coverage. The complete CI matrix, fuzzing and
published-module installation are not represented as local passes here.

## Release sequencing

`server/go.mod` currently requires root library
`v0.7.1-0.20260913221152-66294811b361`. That published version does not contain the
new profile inspector, DDM target resolver or content-cache store APIs. Workspace
builds use `go.work` and the prepared local library, and succeed. A standalone
`GOWORK=off go build -mod=readonly ./...` from `server/` correctly fails at the new
`profile/inspect` import. The dependency was not replaced with a local path or an
invented unpublished version to conceal this.

Before releasing/installing the server through the Go module proxy:

1. Commit and publish the prepared root library changes through the project's
   normal release process.
2. Update the root-library requirement in `server/go.mod`/`server/go.sum` to that
   real version (`go get github.com/deploymenttheory/go-apple-dm@VERSION` from
   `server/` with `GOWORK=off`).
3. Run `python3 scripts/verify-server-module-installation.py`, normal CI and the
   remaining physical acceptance before claiming a released server is ready.

This publication work is separate from the user's local OS upgrade. The built
workspace binaries are ready for the post-reboot lab work. No release, push,
profile assignment, OS installation or reboot was performed during preparation.
