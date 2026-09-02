# 0024: Simulator DDM client and predicate subset

Status: accepted
Date: 2026-09-02
Phase: 5

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/leveraging-the-declarative-management-data-model-to-scale-devices> (predicate-driven activations)
- Doc: <https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest>
- Doc: <https://developer.apple.com/documentation/devicemanagement/status-items>
- YAML: `third_party/device-management/declarative/declarations/activations/simple.yaml` (`Predicate`; installs when the predicate is true or absent; a failing configuration does not block siblings)
- YAML: `third_party/device-management/declarative/declarations/management/properties.yaml` (`@property(key)` values)
- YAML: `third_party/device-management/declarative/declarations/declarationbase.yaml` (reason codes `Info.Predicate`, `Error.ActivationFailed`, `Error.UnknownDeclarationType`)
- YAML: `third_party/device-management/declarative/status/management.declarations.yaml`, `management.client-capabilities.yaml`
- YAML: `third_party/device-management/mdm/checkin/declarativemanagement.yaml` (404 removes a declaration)

## References read

- `fleetdm/fleet@b44343c` `cmd/osquery-perf/ddm.go` (client convergence loop that reports everything valid), `server/service/apple_mdm.go`, `docs/Contributing/architecture/mdm/apple-declarative-device-management.md`
- `jessepeterson/kmfddm@4b75a76` `ddm/declaration.go` (predicates stored verbatim)
- `zentralopensource/zentral@b10dd22` `zentral/contrib/mdm/declarations/protocol.py`
- Record 0004 for the simulator's `Connect` and responder model.

## Known pitfalls found

- Fleet issue 43050: a device loops between `tokens` and `declaration-items` until the server's token settles, so a server that changes the token during a sync wedges the client.
- Fleet FB24193230: an activation with an invalid predicate is accepted by the server and wedges the device's declaration engine until the activation is removed.
- Fleet: a TODO records that removals never appear in status reports, so the server infers them.
- KMFDDM: predicates are stored verbatim with no syntax check.
- Fleet `osquery-perf`: the simulator reports every declaration valid and active, so server-side grading of `Info.Predicate` and `Error.*` reasons was never exercised.

## What they do

- **Fleet osquery-perf**: fetches tokens, items, and each declaration; reports all valid; no predicate evaluation; no removal handling.
- **KMFDDM**: no client; `mdmb` is MDM v1 only.
- **Zentral**: no client; predicate strings are passed to the device as written.

## What we do better

1. `simulator.WithDDM(props)`, `WithDDMMaxRounds`, `WithDDMFaults{DropStatus, StaleToken, FailFetch}`; per-channel state (token, items, declarations, properties, last sync and report); `User` gets the same methods.
2. `SyncDDM` is a bounded convergence loop: `tokens`; stop if unchanged; `declaration-items`; fetch only declarations whose `ServerToken` changed; a 404 removes the declaration; identifiers absent from the manifest are removed; loop until the token settles or `MaxRounds` is reached (`ErrDDMNotSettled`), so a server that never settles is a test failure rather than a hang.
3. `DDMStatusReport(full)` is built from state with Apple's reason codes: `Info.Predicate` on an activation whose predicate is false, `Error.ActivationFailed` with the activation identifier on its configurations, `Error.UnknownDeclarationType`, management declarations always inactive, assets active when referenced; `device.*` items from the identity and `management.client-capabilities` from the registry; incremental reports carry only what changed and full reports carry everything. Every code is one Apple documents in `declarationbase.yaml`: `Info.Predicate` and `Error.ActivationFailed` as above, `Error.MissingConfigurations` (with `ConfigurationIdentifiers`) for an activation whose configuration is absent, `Error.UnableToParsePredicate` and `Error.UnableToEvaluatePredicate` for a predicate the subset rejects or cannot evaluate, and `Info.NotReferencedByActivation` / `Info.NotReferencedByConfiguration` for an unreferenced configuration or asset.
4. `connectAs` runs the sync and posts the report before replying to a `DeclarativeManagement` command, so an e2e scenario sees the whole round trip in one `Connect`.
5. `ddm/predicate` is a public leaf package with a documented NSPredicate subset: `@property(key)`, `@status(path)`, literals, `==`, `=`, `!=`, `<>`, `<`, `<=`, `>`, `>=`, `IN`, `CONTAINS`, `BEGINSWITH`, `ENDSWITH` with optional `[c]`, `AND`/`&&`, `OR`/`||`, `NOT`/`!`, parentheses, `TRUEPREDICATE`, `FALSEPREDICATE`; `SELF`, `MATCHES`, `LIKE`, `BETWEEN`, `SUBQUERY`, `%K`, `$var`, and functions are a named `ErrUnsupported`.
6. `PutDeclaration` runs `predicate.Validate` on `activation.simple`; a failure is `ErrInvalidDeclaration` wrapping `predicate.ErrSyntax` or `ErrUnsupported`, nothing is written, and nothing is notified.

## Verified by

1. `simulator.TestUserChannelDDM`, `simulator.TestDDMFaults` (prove claim 1).
2. `simulator.TestSyncDDMFirstSyncFetchesEverything`, `TestSyncDDMUnchangedTokenSkipsItems`, `TestSyncDDMChangedTokenFetchesOnlyChanged`, `TestSyncDDMRemovalByManifest`, `TestSyncDDM404RemovesDeclaration`, `TestSyncDDMConvergesWithinRounds`, `TestSyncDDMNotSettled`, `TestSyncDDMBadJSON`, `TestSyncDDMServerError` (prove claim 2; `NotSettled` is Fleet 43050 replayed against a stub that never settles).
3. `simulator.TestStatusReportRules`, `TestStatusReportIncremental` (prove claim 3; would fail on Fleet osquery-perf because it reports everything valid).
4. `simulator.TestConnectRunsSyncOnDeclarativeManagement`, `e2e.TestE2E_DDMRoundTrip` (E2E-008).
5. `predicate.TestParseTable`, `TestParseErrors`, `TestEvalTable`, `TestStringRoundTrip`, `FuzzParse` (prove claim 5).
6. `ddm.TestPutDeclaration/InvalidPredicateRejected`, `e2e.TestE2E_DDMPredicate` (E2E-009: an invalid or unsupported predicate is rejected with no event and no push; prove claim 6; would fail on Fleet FB24193230 and on KMFDDM because both store the string verbatim).

## Rejected alternatives

- A full NSPredicate parser: the grammar is large and device behaviour for the rarely used forms is undocumented; the subset is what activations in the wild use, and unsupported forms fail loudly at upload rather than on the device.
- Evaluating predicates server-side to filter the manifest: the device evaluates them; the server validates syntax only, and the simulator evaluates so tests can observe the `Info.Predicate` reason.
- Reporting everything valid (Fleet osquery-perf): grading logic on the server would be untested.
- Running the sync from a background goroutine in the simulator: the `Connect` hook keeps the test deterministic.
