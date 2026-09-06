# 0021: Status reports and status subscriptions

Status: accepted
Date: 2026-09-02
Phase: 5

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/status-items>
- Doc: <https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest> (the `status` endpoint answers 200 with an empty body)
- YAML: `third_party/device-management/declarative/protocol/statusreport.yaml` (`StatusItems`, `Errors[].{StatusItem, Reasons[].{Code, Description, Details}}`, `FullReport`)
- YAML: `third_party/device-management/declarative/status/management.declarations.yaml` (`identifier`, `server-token`, `active`, `valid`, `reasons[].{code, ...}`; management declarations always report `active: false`)
- YAML: `third_party/device-management/declarative/status/management.client-capabilities.yaml`
- YAML: `third_party/device-management/declarative/declarations/configurations/management.status-subscriptions.yaml` (`apply: combined`, set union, items `{Name}`)
- YAML: `third_party/device-management/mdm/checkin/declarativemanagement.yaml` (capabilities arrive only through the `management.client-capabilities` status item)

## References read

- `jessepeterson/kmfddm@4b75a76` `ddm/status.go`, `ddm/path.go`, `storage/mysql/status.go`, `storage/mysql/schema.sql`, `storage/kv/status.go`, `http/ddm/ddm.go`, `http/api/status.go`
- `fleetdm/fleet@b44343c` `server/service/apple_mdm.go` (status report handler), `server/datastore/mysql/apple_mdm.go` (`MDMAppleStoreDDMStatusReport`)
- `zentralopensource/zentral@b10dd22` `zentral/contrib/mdm/declarations/status_report.py`, `zentral/contrib/mdm/declarations/management.py` (`build_target_management_status_subscriptions`)

## Known pitfalls found

- KMFDDM: `StatusItems` other than `management.declarations` are flattened to dotted paths, losing array indices and nulls; unhandled paths are debug-logged and dropped.
- KMFDDM: `status_values.value` is `VARCHAR(255)`, so long values are truncated silently.
- KMFDDM: retention by a per-insert `row_count` bump; the status body is read without a size limit; a malformed report answers 500 (the device retries forever); no per-enrollment delete.
- Fleet: only `management.declarations` is graded and only for configurations and management; rows are matched by token, so a colliding or stale token grades the wrong row; `Errors[]` and `FullReport` are ignored; removal is inferred empirically from absence in any report.
- Zentral: `None` versus `[]` distinction had to be added after empty reports were treated as missing; one declaration version can appear under two server tokens in one report and must be deduplicated; capability reads must be defensive because devices have sent partial `management.client-capabilities`.
- Apple: `Errors[].Reasons[]` keys are capitalised (`Code`, `Description`, `Details`) while nested `management.declarations[].reasons[]` keys are lower case; `FullReport` is a safety sync about every 24 hours that replaces all status.

## What they do

- **KMFDDM**: `ddm/status.go` walks the report, stores declaration rows keyed by identifier plus token, flattens values, keeps errors in a table with hard-coded offset and limit.
- **Fleet**: parses `management.declarations`, updates `host_mdm_apple_declarations` status by token, discards the rest.
- **Zentral**: grades all four sections, stores the raw report, deduplicates by identifier preferring the current token, builds `management.status-subscriptions` from `supported-payloads.status-items` with an 11-item baseline when capabilities are unknown.

## What we do better

1. The report body is limited (default 1 MiB, `ErrStatusTooLarge` maps to 400) and decoded with `encoding/json/v2` strict defaults, so duplicate names and invalid UTF-8 are rejected as 400 rather than accepted or answered 500.
2. `StatusItems` is walked recursively and item boundaries are resolved through the `schema/status` registry, so nested paths (`management.declarations` under `StatusItems.management`) are typed and unknown paths are retained rather than dropped.
3. Per-declaration rows are keyed by `(kind, identifier)`; when one identifier appears under two tokens the row for the snapshot's token wins.
4. Every other item is stored as `(path, canonical value)` in bytes, including arrays and nulls, with no length cap below the column type.
5. `Errors[]` is stored per report; raw reports are retained as the last N by sequence.
6. `FullReport` replaces the enrollment's status atomically; identifiers absent from a full report are removed and published as `DDMStatusReceived.Data.Removed`, so removal is a fact from the device rather than an inference.
7. `ClientCapabilities(ctx, id)` decodes the stored `management.client-capabilities` item defensively (missing or malformed sub-keys yield an empty capability set, never an error on the check-in path).
8. When `Config.Subscriptions.Enabled` is set, every manifest carries a synthesised `com.apple.configuration.management.status-subscriptions` with identifier `com.deploymenttheory.mdm.status-subscriptions` whose `StatusItems` are the reported `supported-payloads.status-items` filtered by `Exclude` prefixes (default `test.`), or the 11-item baseline (`device.identifier.{serial-number, udid}`, `device.model.{family, identifier, marketing-name}`, `device.operating-system.{build-version, family, marketing-name, version}`, `management.client-capabilities`, `management.declarations`) when capabilities were never reported; its `ServerToken` is `TokenFor` of the canonical declaration, so a capability change re-syncs once and converges; an admin declaration with the same identifier takes precedence.
9. The status endpoint answers 200 with an empty body as Apple specifies.

## Verified by

1. `ddm.TestStatus/TooLarge`, `/DuplicateKeyRejected`, `/InvalidUTF8Rejected` (prove claim 1; would fail on KMFDDM because the body is unbounded and malformed input answers 500).
2. `ddm.TestStatus/NestedItemsResolved`, `/UnknownPathsRetained` (prove claim 2; would fail on KMFDDM because unhandled paths are dropped).
3. `ddm.TestStatus/DuplicateIdentifierPrefersSnapshotToken`, `ddmtest.RunAll/Status/KeyedByIdentifierNotToken` (prove claim 3; would fail on Fleet because rows are matched by token).
4. `ddmtest.RunAll/Status/ArraysAndNullsPreserved` (prove claim 4; would fail on KMFDDM because flattening loses both and the column is 255 bytes).
5. `ddm.TestStatus/ErrorsStored`, `ddmtest.RunAll/Status/ErrorsNewestFirst`, `/Status/RetentionKeepsNewestN` (prove claim 5; would fail on Fleet because `Errors[]` is ignored).
6. `ddm.TestStatus/FullReportRemovesAbsent`, `/PartialKeeps`, `/PublishesEvent`, `ddmtest.RunAll/Status/FullReportDeletesAbsentRowsAndValues`, `/Status/PartialUpsertKeepsFirstSeen` (prove claim 6; would fail on Fleet because `FullReport` is ignored, and on KMFDDM because there is no replace).
7. `ddm.TestClientCapabilities/Decoded`, `/MissingIsEmpty`, `/MalformedIsEmpty` (prove claim 7).
8. `ddm.TestSubscriptions/ConvergesAfterCapabilitiesReport`, `/BaselineWhenUnknown`, `/DefensiveAgainstBadCapabilities`, `/ExcludesTestItems`, `/AppearsInManifestAndItems`, `/AdminDeclarationWins` (prove claim 8; the baseline and defensive cases are Zentral's regressions replayed).
9. `inproc.TestHandler/Status` (empty body, 200) in 0023.

## Rejected alternatives

- Grading only `management.declarations` (Fleet): everything else a device reports would be lost, and `management.status-subscriptions` exists precisely to ask for more.
- Flattening values to dotted keys (KMFDDM): array order and nulls are data; canonical bytes per path keep them and still allow a query by path.
- Matching declaration rows by token (Fleet): the identifier is the identity; the token is a version.
- Answering 500 on a malformed report (KMFDDM): the device retries, so the answer must be 400.
- Always sending the baseline subscriptions: devices that report capabilities can be asked for everything they support, and the synthesised token converges after one round.
