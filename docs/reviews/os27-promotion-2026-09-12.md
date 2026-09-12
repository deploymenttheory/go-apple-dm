# OS 27 adoption and incident completion

The normal library combines Apple OS 27 commit
`b0180185a5e4077070710033341b71d0cbe1a18a` with historical release commit
`67045e2fa06f528b196c01edee6a8bf88b844beb`. Both are pinned submodules and recorded
in generated provenance. This report supersedes the pre-adoption status in the
[initial assessment](apple-schema-incidents-2026-09-12.md) and the
[mixed-fleet follow-up](mixed-os-fleet-2026-09-12.md).

## Public behavior

The generated API adds 813 declarations compared with branch commit `3b66660`,
with zero removed declarations and zero changed signatures or serialization tags.
No removal allowance is added. Historical fields, legacy profile URL encoding,
and supported older-device targets are retained. New OS 27 fields and command
types are available through ordinary imports.

Known commands require valid payloads. Target checks use device inventory and
observed capabilities, and dispatch checks queued commands again after inventory
updates. Unsupported work is individually cleared through `storage.CommandClearer`,
retaining its audit row and a `command-rejected` event. Custom stores without that
optional interface return an error instead of clearing unrelated commands. No
database migration is required. Inventory persistence failures roll back both
the inventory change and command acknowledgment; a later retry can succeed.

The monitor preserves the published historical pin. Older stable snapshots use
the retained release as a comparison baseline, produce no downgrade patch, and
cannot certify compatibility with the published OS 27 API. Stable adoption checks
resume when Apple's release includes the adopted seed. `make test` requires all
eight OS 27 contracts, including explicit passing events for every test.

## Incident evidence

| Incident | Resolution and executable evidence |
|---|---|
| #40 — Availability | Source-derived boundary probes and `TestSeedOS27SoftwareUpdateRemoval`, `TestSeedOS27MixedFleet`, `TestSeedOS27UpgradeRechecksQueuedCommands`, and `TestSeedOS27LegacyProfileCompatibility` cover removals, retained older-device access, channels, enrollment restrictions, and upgraded inventory. |
| #41 — Enhanced logging | `TestSeedOS27EnhancedLogCommands` covers trigger/cancel delivery, required AppleCare tokens, supported targets and responses. `TestSeedOS27EnhancedLoggingStatus` retains all ten states plus token/timestamp and partial/full report behavior. Devices and AppleCare handle log upload. |
| #42 — Managed Apple Account JWT | `GetTokenHandler` requires caller-issued RS256 tokens. `gettoken_contract_test.go` signs and independently verifies the token against its certificate, rejects tampering and checks exact plist transport. Apple certificate registration remains caller-owned. |
| #43 — Enrollment retry | The normal generated response exposes `ShouldRetryEnrollment`. `TestSeedOS27ReturnToServiceRetry` verifies omitted/false/true wire values, bootstrap-token preservation, and iOS 27 availability. Deployment policy selects eligible targets. |
| #44 — Timestamp metadata | Typed `ReasonDetail.ValueType` retains the annotation while preserving the string type. Parser and generation regressions reject unknown metadata and keep annotations out of protocol fields. |
| #45 — Examples metadata | Typed example/file structures preserve strict decoding and reference auditing. Fixtures cover empty/populated examples, malformed structures and unsupported keys; the recorded candidate contains 304 occurrences. |
| #46 — Content-cache metrics | [Decision 0051](../research/decisions/0051-content-cache-metrics.md) includes an opt-in library with 88 report properties, parent/peer records, strict decoding, validation and POST/PUT receiver contracts. `TestSeedOS27ContentCacheContract` checks the retained Apple fixture. Consumers supply authorization, TLS and acceptance/storage; no reference-server route is installed. |

## Validation record

Fresh logs and machine-readable evidence are retained locally under
`cover/os27-promotion/`. The immutable final monitor manifest records project
commit `6601f08e3682430efc8a39e9dffc5c0ebc9e61bb`, which contains the promoted
library and monitor implementation. The server requires its published
pseudo-version `v0.5.1-0.20260912215028-6601f08e3682`. The final dependency and
report commit changes no library or server runtime code.

| Check | Result |
|---|---|
| Deterministic generation and API comparison | Passed; 813 additions, zero removals and zero changed declarations against `3b66660`. |
| Unit tests | Both modules passed with race detection and shuffle; all eight required OS 27 contracts emitted passing events. |
| Database integration | Passed against disposable PostgreSQL and MySQL, plus SQLite and in-memory storage contracts. |
| E2E | Passed with SQLite, PostgreSQL and in-memory stores, including the separate DDM server container. |
| Process acceptance and bench documentation | Passed; actual `dmserver` and `dmctl` processes exercised. |
| Storage performance | Cleared 100,000 PostgreSQL commands in 873 ms, within the existing one-second gate. |
| Fresh combined coverage | **95.85%** overall; every non-exempt package meets the existing 95% floor. `sqlcommon`: **95.07%**; content-cache: **98.17%**. |
| Lint | Both modules passed with zero issues. |
| Monitor Python tests | All **51** passed. |
| Immutable stable and OS 27 assessments | Every stage passed. Older stable is comparison-only; neither result produces a patch. The historical pin is retained. |
| Source-derived availability | All **45,288** probes passed against the combined generated tables. |
| Publication preview | Report-only mode retained the five engineer-review findings and zero failure findings; no GitHub writes. These reviews are addressed by the linked incident resolutions above. |
| Decoder fuzzing | Passed **2,039,229** executions in the 20-second fuzz run. |
| Independent server installation | With `GOWORK=off` and no replacements, resolved the exact declared library, built public consumer/server packages and installed both commands. |
| Independent runtime contracts | Service and in-process DDM adapter tests passed with the OS 27 tag and `GOWORK=off`, using the published library dependency. |

Coverage combines only this run's unit, storage, and three E2E layers. No coverage
exemptions or thresholds changed. Earlier failed runs were replaced before the
successful gate; API comparison sources are excluded from Go package discovery.

The schema-generation diagram passed all nine artifact checks with zero errors
or warnings, plus browser interaction checks. Browser measurements at all four
required viewport sizes and both boundary themes found no horizontal overflow.
The visual checker reports the vertical scrolling allowed by the repository's
reading policy; the captured light/dark layouts were inspected.

These tests establish the listed contracts. No physical-device or Apple account
registration verification is claimed.
