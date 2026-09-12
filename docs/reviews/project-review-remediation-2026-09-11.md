# Project review defect remediation — 11 September 2026

This phase fixes five reproduced engineering defects from the review of commit
`538e709ed3fb499cb2178edf06d8cf87f4eec2c4`. Its purpose is to make the server
installable with its declared dependencies, bound asynchronous event delivery,
correct projection removal, repair container health probing, and expose the full
lint baseline. The implementation and local verification are complete; merging
and normal versioned releases are subsequent steps.

## Findings, changes and evidence

| Finding | Reproduced problem | Implemented behavior | Verification |
| --- | --- | --- | --- |
| R1: module installation | The server required an older root library that lacked APIs used by its public packages; checkout builds masked the mismatch. | `server/go.mod` requires a published library commit containing the required APIs and has no local replacement. CI checks dependency resolution, package builds and command installation outside the repository workspace. | The packaged candidate builds against exactly its declared library version; both `dmserver` and `dmctl` install successfully. |
| R2: asynchronous event delivery | Blocked sinks allowed concurrent deliveries to grow with publications. | Fixed workers, a bounded pending queue, an event lifetime, immediate overload rejection and outcome counters. Application construction failures release owned workers. | Race tests cover saturation, recovery, expiry, concurrent publication/shutdown and cleanup. Status and logging tests cover limits, counters and warning throttling. |
| R3: projection removal | Registering a nil projection left the previous field projection active. | Nil registration removes the previous projection while retaining the known event type; subsequent lookups produce metadata only. | Replacement in both directions and concurrent registration/lookup pass under the race detector. |
| R4: container health | The image's HTTP probe failed against a working server using native TLS. | `dmserver -check auto` follows the configured listener and port, verifies TLS against the configured certificate and checks `/healthz`. | The shipped Docker health command reports healthy with native TLS, including a non-default port. Probe tests cover private trust, certificate mismatch/expiry, redirects, deadlines and unavailable storage. |
| R5: lint baseline | Differential lint hid 30 existing diagnostics. | Both modules use full-baseline lint; diagnostics are corrected or narrowly justified while preserving configuration keys. | Both module runs pass with zero issues and automatic fixes disabled. |

R1 is an integration defect; R2 is a reliability defect demonstrated with blocked
test sinks; R4 is an operational defect; R5 is maintenance. R3 is a conditional
privacy defect because a requested projection removal did not take effect.
The review did not demonstrate a practical denial-of-service attack or an actual
secret disclosure. These changes do not establish production security assurance.

## Which queue changed

The changed queue delivers asynchronous events to subscribers, including audit
and webhook sinks. It is separate from DDM change storage and device command
queues. Defaults are eight workers, 1,024 pending events and a 30-second lifetime
from acceptance. The server exposes configuration and cumulative outcomes through
authenticated `/admin/v1/config` and `dmctl status`.

Audit and webhook subscribers share this capacity. A rejected, expired or failed
event can leave an audit gap. Shutdown timeout and process failure can also lose
events. There is no durable replay. Capacity bounds event count rather than payload
bytes, and a handler that ignores cancellation retains its worker. This phase
provides finite concurrency and visible delivery outcomes, not guaranteed delivery.

## What server module installation verification means

Run `make verify-server-module-installation`. Its output identifies the server
artifact and declared root library version, then reports four stages:

1. Resolve dependencies with `GOWORK=off`, reject replacements and require the
   selected root library to match the server's declared version exactly.
2. Build a separate application importing public service, HTTP, storage and DDM
   adapter packages from the server module.
3. Build all server packages and confirm the root library version remains unchanged.
4. Run `go install` for `dmserver` and `dmctl` into a temporary directory.

By default, only the candidate server sources are packaged into a temporary module
proxy. The root library and other dependencies resolve through the configured Go
proxy with public checksum verification. The synthetic server artifact has a
content-derived version and is exempt from public checksum lookup. The check
compiles and installs commands; runtime behavior has separate test suites.

`python3 scripts/verify-server-module-installation.py --server-version vX.Y.Z`
instead verifies an already published server module, including its public checksum.
The **go | Published server module installation** workflow runs this mode after
`server/v*` tags and on manual dispatch. It detects publication problems after the
tag exists. The candidate check runs in Go Test before merge.

## Dependency publication and merge requirement

The root library implementation is published at commit
`816e0f2370fbbcd2727fb4d1e9dde2333b7b8abf`. The server requires its canonical Go
pseudo-version, `v0.3.4-0.20260911195450-816e0f2370fb`, which resolved successfully
through the public Go infrastructure. Normal tags and changelogs remain managed
by release-please; no normal release was published during this phase.

Merge this branch with a merge commit to retain the pinned library commit in main's
history. Before squashing instead, replace the dependency with a published normal
root release containing these APIs and repeat installation verification. Verification
of the next normal server release remains a post-publication task.

## Completed local validation

Both modules passed their race-enabled, shuffled unit suites. Shared storage
contracts and SQL integration passed with SQLite, PostgreSQL 17 and MySQL 8.4.
Simulator scenarios, the container-backed DDM split deployment, and real-process
combined/split server and CLI acceptance passed. The actual Docker health command
passed with native TLS at the standard test port and port 18443; temporary test
containers were removed afterward.

Merged coverage is **95.65%**, with every non-exempt package meeting the 95% floor.
Full-baseline lint, generated-output verification, workflow checks and `actionlint`
passed. The renamed installation command passed against candidate
`v0.0.0-review.a332c9dca02603ab` and the declared library version above.
Local logs and coverage are retained under `cover/remediation-20260911/`.

The draft PR's standalone gosec scan identified two additional diagnostics: a
signed-to-unsigned conversion in shutdown accounting and a configured certificate
file read. Shutdown now checks the pending count before conversion. The file read
has a line-specific G304 justification because its path comes from operator TLS
configuration, with no remote request input. Standalone gosec v2.29.0 passes for
both modules with `GOWORK=off`; targeted event and probe race tests also pass.

Historical database upgrades, physical-device interoperability, representative
fleet load and durable audit delivery remain outside these five fixes. The original
review scores have not been recalculated from this remediation run.
