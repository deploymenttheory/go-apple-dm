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
`cover/os27-promotion/`. Generated-output verification, the public API comparison,
all eight OS 27 contracts, 50 monitor tests, and the inventory rollback regression
passed during promotion. Full integration, coverage and independent installation
results are recorded when the final validation completes.

The schema-generation diagram was regenerated with all nine artifact checks
passing, without errors or warnings. Browser interaction checks passed.

These tests establish the listed contracts. No physical-device or Apple account
registration verification is claimed.
