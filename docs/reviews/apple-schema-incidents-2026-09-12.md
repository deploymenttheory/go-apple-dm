# Apple schema incident remediation — 12 September 2026

Branch: `fix/apple-schema-incidents-40-46`.

The subsequent [mixed-fleet follow-up](mixed-os-fleet-2026-09-12.md) supersedes
the seed API blockers recorded in this initial assessment. It preserves older
wire contracts while adding OS 27 compatibility.

This change prepares compatibility with Apple's OS 27 seed and adds an opt-in
content-cache library. The stable submodule pin and stable generated API remain
at `67045e2fa06f528b196c01edee6a8bf88b844beb`. The assessed seed is
`b0180185a5e4077070710033341b71d0cbe1a18a`. Implementation commits are `8395624`
and `78938fa`; the latter includes the bench adjustments needed by central
validation. No release version or server dependency requirement changes.

## Incident outcomes

| Issue | Implementation and evidence |
|---|---|
| [#45 — Examples](https://github.com/deploymenttheory/go-apple-dm/issues/45) | Explicit example/file metadata structs parse all 304 recorded occurrences, including request/response references. Tests cover empty/populated examples, malformed shapes, unknown nested keys, and missing/invalid references. Stable generated output is unchanged. |
| [#44 — Timestamp annotation](https://github.com/deploymenttheory/go-apple-dm/issues/44) | `ReasonDetail.ValueType` retains the annotation in both affected source files while `Type` remains `<string>`. A generation regression proves the annotation does not change protocol fields or reason vocabulary output. Apple's recorded meta-schema omits this key; its use in the source files is handled explicitly. |
| [#40 — Availability](https://github.com/deploymenttheory/go-apple-dm/issues/40) | All 45,256 source-derived boundary/context probes pass against compiled seed tables. Service tests prove deprecated software-update commands remain usable on macOS 26.4 and are rejected at 27.0. Central enqueue validation also checks present payload fields against each target. True upstream API removals remain adoption findings below. |
| [#41 — Enhanced logging](https://github.com/deploymenttheory/go-apple-dm/issues/41) | Candidate tests deliver trigger/cancel commands and retain acknowledgments/errors. Missing AppleCare tokens are rejected. Tests cover OS 26/27, supervision, unsupported platforms, macOS user versus device channels, Shared iPad and user enrollment. DDM adapter tests retain all ten logging states with AppleCare token and timestamp, including partial/full report semantics. Log upload remains a device/AppleCare responsibility; no additional upload endpoint is implied. |
| [#42 — Managed Apple Account token](https://github.com/deploymenttheory/go-apple-dm/issues/42) | The `GetTokenHandler` contract identifies the caller as issuer and requires RS256 with the registered RSA certificate and Apple claims. A fixture signs a JWT, independently verifies the transported signature against its certificate, rejects signature tampering, and proves exact `TokenData` preservation through plist serialization. Certificate registration with Apple remains caller-owned and is not simulated as verified. |
| [#43 — Enrollment retry](https://github.com/deploymenttheory/go-apple-dm/issues/43) | Candidate service tests check actual plist presence and values for omitted/false/true `ShouldRetryEnrollment`, while preserving stored and handler-supplied bootstrap tokens. Generated checks cover the iOS 27 boundary and enclosing supervision/ADE restrictions. Deployment policy selects the option; the stable generated response does not expose it yet. |
| [#46 — Content-cache metrics](https://github.com/deploymenttheory/go-apple-dm/issues/46) | Support is included as an independent library. Its 88 report properties, parent/peer records, decoder, validator and receiver follow a retained Apple OpenAPI fixture. Authorization and acceptance callbacks are required. POST and PUT are supported because Apple's OpenAPI and declaration disagree on method. Authentication, HTTPS, storage and delivery guarantees belong to the embedding application. See [decision 0051](../research/decisions/0051-content-cache-metrics.md). |

The typed seed scenarios are stored behind `schema_seed_os_27`. The monitor
enables that tag only for the OS 27 seed and requires explicit passing events for
all five contract tests. A skipped or missing test cannot count as successful
candidate verification. Stable assessments compile without seed-only symbols.

## Availability review coverage

These are the original issue's groups; counts refer to distinct schema objects.
Their complete path-level evidence is retained in the assessment's `audit.json`
and `boundaries.json`.

| Group | Objects / files | Result |
|---|---|---|
| Removal boundaries | 23 / 9 | Compiled support rejects targets at and after removal; service tests cover all four affected software-update command types. |
| Deprecation boundaries | 16 / 9 | Deprecation is reported without making an otherwise supported target unavailable. |
| Introductions and availability | 38 / 19 | Source-derived probes cover previous and new version boundaries on each affected platform. |
| Enrollment and channel restrictions | 15 / 7 | Probes cover supervision, ADE, user approval, device/user scope, Shared iPad and user enrollment. Enhanced-log service tests exercise actual macOS user-channel dispatch. |
| Removed metadata or fields | 12 / 7 | Removed restrictions use the resulting inherited support metadata. Actual field/API removals are recorded separately by the API guards; this branch does not authorize their adoption. |

## Public behavior and compatibility

`Core.Enqueue` validates the bytes the device will receive, checks that their UUID
and request type match the command envelope, and validates known payloads before
storage. Invalid input returns `CodeBadRequest` with schema errors preserved for
callers. Per-target command or populated-field failures are reported in `Skipped`.
Disabling `ValidateTargets` bypasses availability checks but retains required-input
and value validation. Unknown command types and extension wire bytes retain their
existing transport behavior; generated validation covers modeled properties.

Previously incomplete inventory requests in tests and executable bench scenarios
now supply `Queries`. This is a behavior tightening for service callers that
previously queued incomplete known commands. No database migration is required.

Apple's title change for the content-caching profile is handled with an explicit
generator name override, preserving `profiles.ContentCaching` and its nested
names. Examples and timestamp annotations are documentation metadata; they do not
appear in generated protocol fields.

The content-cache package uses `Decode` for incoming reports and Go 1.27
`encoding/json/v2` tags for marshaling, including retained unknown properties.
Its default request limit is 1 MiB. The receiver returns 202 after successful
acceptance, 400 for invalid input, 401 for authorization rejection, 405 for
unsupported methods, 413 for excessive size, 415 for unsupported media type and
503 for unavailable acceptance. It installs no `dmserver` route.

## Seed adoption findings

Stable assessment passes every stage. The seed passes snapshot, audit, parsing,
generation, build, boundary and runtime-test stages, including all five required
contract tests. Seed API and exported-name verification deliberately remain failed:
Apple removed 12 exported declarations and changed three public field types/tags.
They are separate adoption findings, not parser or runtime test failures.

The removed declarations are the old software-update `RecommendationsCadence`
response field, managed-app `VPPType`, five manifest fields plus their nested items
type, Finder `InterfaceLevel`, Firewall `EnableLogging` and `LoggingOption`, and
SystemLogging `Processes`. Changed declarations are the two legacy-profile
`ProfileURL` fields becoming optional pointers and the parental-control `WhiteList`
element type changing. The exact before/after signatures are in `api.json`.

These changes require an explicit migration decision before seed adoption.
`ALLOWED_REMOVALS.md` and the stable generated files are unchanged; passing
runtime tests does not authorize those public API changes.

## Validation artifacts

Local logs, immutable discovery manifests, full source evidence and assessment
results are retained under `cover/incidents-20260912/`. `final-assessments/`
records project commit `78938fa`. These artifacts are gitignored; this document
records the reviewable outcomes. The subsequent completion commit only formats
three bench calls and adds this report.

| Check | Result |
|---|---|
| Full unit suite (`make test`) | Passed for both modules with the race detector. |
| Database integration (`make test-storage`) | Passed against disposable PostgreSQL and MySQL databases, plus SQLite. |
| E2E (`make test-e2e`) | Passed with SQLite and in-memory storage. |
| Process acceptance (`make test-acceptance`) | Passed, including real server and CLI processes. |
| Combined fresh coverage gate | Passed at **95.67%** overall; every non-exempt package meets the existing 95% minimum. The new content-cache package reaches **98.17%**. Only this run's unit, storage and two E2E layers were merged. |
| Lint and stable generation verification | Passed; stable generated output and removal guards remain unchanged. |
| Schema monitor Python tests | All **45** passed. |
| Source-derived availability probes | All **45,256** passed. |
| Required OS 27 contract tests | All **five** emitted passing events in the candidate assessment. |
| Content-cache decoder fuzzing | Passed **1,241,996** executions in the 20-second run. |
| Independent server module installation | Passed with `GOWORK=off`, including both commands and public packages. |
| Monitor publication preview | Completed in report-only mode, producing ten proposed issues without remote writes. |

The stable assessment is fully green. The seed's API and exported-name failures
listed above are the remaining prerequisites for a future pin update; this branch
does not claim that seed adoption is ready.
