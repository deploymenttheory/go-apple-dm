# 0041: Apple's closed vocabularies as Go constants

Status: accepted
Date: 2026-09-03
Phase: 9

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/statusreport>
- Doc: <https://developer.apple.com/documentation/devicemanagement/status-items>
- Doc: <https://developer.apple.com/documentation/usernotifications/handling-notification-responses-from-apns>
  (the 32 APNs reason strings and the status each is paired with)
- YAML: `third_party/device-management/declarative/declarations/declarationbase.yaml` (24 reason
  codes, the vocabulary of the `management.declarations` status item)
- YAML: `third_party/device-management/declarative/status/app.managed.list.yaml` (14 codes),
  `package.list.yaml` (2 codes)
- YAML: `third_party/device-management/declarative/status/statusreason.yaml` — the wire shape
  (`code`, `description`, `details`) and the sentence that decides the scoping question below
- YAML: `third_party/device-management/declarative/status/management.declarations.yaml`
  (`reasons[].code` under every declaration kind)

## References read

- `korylprince/go-adm@7a87c98afb418bebb6c3f94b9edacf634ce55a2c` `schema/schema.gen.go:124,287-295`,
  `schema/schema.go:91-99`, `generated/declarative/status/status.go:373-385,445-452`
- `jessepeterson/admgen@b26a7609b0a326988e3cea8853de5ee0061f8fab` `cmd/admgencmd/builder.go:41-45,520-528`
- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a` `server/fleet/apple_mdm.go:1416,1436-1467`,
  `server/service/apple_mdm.go:8046-8172`, `server/mdm/apple/util.go:174-180`,
  `server/mdm/apple/apns_errors.go`, `server/mdm/apple/apple_mdm.go:1843-1879`
- `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151` `ddm/status.go:21-28,50-54,82-85,95-102`,
  `storage/mysql/status.go:37,253-254`
- `zentralopensource/zentral@6b93d01d1bc8471ed98807b02a26b83452e8c8b7`
  `zentral/contrib/mdm/declarations/linkers.py:33-51`, `declarations/status_report.py:15-32`,
  `zentral/contrib/mdm/apns.py:59-77`, `schema_data/declarative/declarations/declarationbase.yaml`
- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10` `push/nanopush/provider.go:41-66`,
  `push/push.go:12-15`, `mdm/checkin.go:96-104`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107` `platform/apns/service.go:97-101`,
  `platform/apns/push.go:80-83`
- `micromdm/nanohub@3d73c1a83d5a042bfa5d31ba98d32de996007667` `ddmadapter/ddmadapter.go:10,116-136`
- `jessepeterson/mdmcommands@0ef71b4590d729da33dcb653c5f7d8e6bdce6624` `generate.go:9`, `cmd_device.go:825-841`
- Ours: `internal/schemagen/model.go:60,221-232`, `emit_registry.go`, `docs/research/decisions/0007-apns-push.md`

## Known pitfalls found

- **`korylprince/go-adm` parses `reasons:` and never reads it.** `schema/schema.gen.go:124` declares
  `Reasons []*Reasons` with a full `Value`/`Description`/`Details` element type at 287-295, and
  `grep '\.Reasons'` across `schema/`, `generator/`, `cmd/` and `utils/` returns **zero** hits. The
  cause is at `schema/schema.go:91-99`: `IsEnum()` fires only on a `rangelist`, and Apple puts these
  values in a sibling top-level `reasons:` block instead. So go-adm emits constants prolifically —
  `AppManagedListStatusValueState`, `InstallApplicationCommandRejectionReason` with ten — and
  exactly none for the reason vocabulary. **Our generator was in precisely this state**
  (`internal/schemagen/model.go:60` parsed `Reasons` into `model.Reason`; nothing emitted it), which
  is what this record fixes. Two independent generators reaching the same dead end is the argument
  for a test that pins the constants and the table as one set.
- `jessepeterson/admgen` has **no `reasons` yaml tag at all** (`cmd/admgencmd/builder.go:41-45`), and
  even for a `rangelist` it emits a **comment, not constants** (`builder.go:520-528`), which is why
  `mdmcommands/cmd_device.go:830` reads `RequestType string // supported value: DeclarativeManagement`.
- `zentral` vendors `declarationbase.yaml` *including* all 24 codes, and its loader reads only
  `payload` and `payloadkeys` (`linkers.py:33-51`) — the `reasons:` block is never touched. Its
  `update.sh` does not copy `declarative/status` at all, so the app-list and package-list
  vocabularies are absent from the tree entirely.
- `fleet` parses `MDMAppleDDMStatusReport.Errors` (`server/fleet/apple_mdm.go:1448`) and **never
  reads it**: the only construction site is `server/service/apple_mdm.go:8047`, and
  `handleDeclarationStatus` touches only `StatusItems.Management.Declarations`.
- **`schema/status` already exports `Reason`** — Apple's own wire struct from `statusreason.yaml`
  (`schema/status/types.gen.go:1490`). A generated vocabulary type named `Reason` would collide, so
  the emitted names are `ReasonEntry` and `ReasonDetail`.
- **A reason code is not unique within a package.** `Error.DownloadFailed` and `Error.InstallFailed`
  are each declared by both `app.managed.list.yaml` and `package.list.yaml` with different prose. A
  `map[string]Entry` keyed by code would silently drop one meaning, and a constant per declaration
  would not compile.
- `internal/schemagen/emit.go`'s `files()` returned a fixed map. A package with no reasons must emit
  no file rather than an empty vocabulary, which would read as "Apple defines none" instead of
  "Apple defines none here"; `Write`'s `removeStale` already deletes a `.gen.go` that is no longer
  produced, so a conditional entry is safe.
- `make generate` ran `go generate ./...` against a tree with **no `go:generate` directives**, so the
  command CLAUDE.md documents for regenerating the schema regenerated nothing. Found while testing
  this change.

## What they do

- **fleet**: the only project with any DDM reason code as a named constant — **2 of 24**,
  `MDMAppleDDMReasonPredicate` and `MDMAppleDDMReasonActivationFailed` at
  `server/fleet/apple_mdm.go:1451-1455`, with `Error.UnknownDeclarationType` (which *is* one of
  Apple's 24) left as an inline literal at `server/service/apple_mdm.go:8200`. `Code` is a plain `string`, though the same file
  gives `MDMAppleDeclarationValidity` a defined type with constants. It is also the only project to
  attach semantics to a code: an `Error.ActivationFailed` correlated with an `Info.Predicate` on the
  activation named in `Details["Identifier"]` is downgraded from failed to **verified**
  (`apple_mdm.go:8140-8172`). Reasons reach the UI as a formatted string
  (`server/mdm/apple/util.go:174-180`), never as a code, and no metric is labelled by one.
- **kmfddm**: `ReasonsJSON json.RawMessage` (`ddm/status.go:21-28`), stored and returned as an opaque
  blob; failure is decided structurally (`!d.Active && d.Valid != "valid"`, `status.go:95-102`)
  rather than from any code. No prometheus dependency in the repo.
- **nanohub**: imports kmfddm's `ddm` package wholesale (`ddmadapter/ddmadapter.go:10`), so it
  inherits the blob.
- **zentral**: passes `reasons` through into an `extra_info` JSONField and pretty-prints it in the
  device detail template; status is decided from `valid`/`active` only.
- **nanomdm, micromdm**: the DDM payload is `Data []byte` / `data []byte` and is never unmarshalled.
- **APNs**: fleet defines **5 of 32** reason strings (`server/mdm/apple/apns_errors.go`) and branches
  on exactly one — `Unregistered` turns MDM off for the host (`apple_mdm.go:1860`); the other four are
  declared and never compared outside tests. nanomdm parses `reason` into `JSONPushError`
  (`push/nanopush/provider.go:41-45`) and only concatenates it into a message — it branches on
  neither the reason **nor** HTTP 410, and `push.Response` is `{Id, Err}` with no room to say
  "invalid". micromdm reduces the push result to a bare error and prints it. zentral never calls
  `r.json()` at all (`apns.py:59-77`): a 410 and a 400 are both "stop retrying", and no token is
  ever marked invalid.
- **Nobody** exposes a reason code or an APNs reason as a metric label, and nobody generates the
  DDM vocabulary from Apple's schema.

## What we do better

1. **The vocabulary is generated, so it cannot drift from Apple.** `admgen` emits `reasons.gen.go`
   for any family whose schemas declare a `reasons:` block — today `schema/ddm` (24 codes) and
   `schema/status` (16), against fleet's 2 hand-written ones. `make verify` fails if regeneration
   would change a byte, so a schema refresh that adds a code adds a constant in the same commit.
2. **A removed code is a deliberate decision, not a silent one.** Every constant enters
   `schema/EXPORTED_IDENTIFIERS.lock` (46 new names), so a code Apple withdraws fails `admgen verify` until it is
   listed in `ALLOWED_REMOVALS.md`. No reference has any guard here because no reference has the names.
3. **The scoping follows Apple rather than convenience.** `statusreason.yaml` states that "each
   status item defines its own set of `code`, `description`, and `details` values", so `Reasons` is
   `map[string][]ReasonEntry` and every entry carries the schema that declares it. Both meanings of
   `Error.DownloadFailed` survive; fleet's flat `Code string` and kmfddm's blob cannot express the
   distinction, and go-adm generates a separate anonymous struct per status item with no shared
   vocabulary at all.
4. **The constants and the table are one set, and a generated test says so.** Each package's
   `TestConformanceReasons` asserts every constant is a key of `Reasons`, the entry count, that no
   entry is missing its code, schema or description, and that `ReasonCodes()` is sorted and complete.
   That is what lets a caller treat `Reasons` as *the bound* on the vocabulary — the property the
   phase 9 cardinality rule needs — rather than as a sample of it.
5. **The APNs vocabulary is complete: 32 of 32**, transcribed from Apple's own table with the status
   each reason is paired with, against fleet's 5 and everyone else's none. It includes
   `UnrelatedKeyIdInToken` and `BadEnvironmentKeyIdInToken`, which are absent from every reference.
6. **The reason text is Apple's, and it is attached.** A constant documents what the code means
   rather than restating its own name, and a code with two meanings says so instead of picking one:
   `ReasonErrorDownloadFailed is Apple's "Error.DownloadFailed". Its meaning depends on the status
   item reporting it; see Reasons.`
7. **`make generate` regenerates the schema.** It invokes `cmd/admgen` instead of a `go generate`
   that matched no directives.

## Verified by

1. `schemagen.TestGenerateWholeTree` (asserts `ddm/reasons.gen.go` and `status/reasons.gen.go` are
   produced, that `commands` gets none, and that the reason constants are in `EXPORTED_IDENTIFIERS.lock`) and
   `admgen verify` (proves claim 1; would fail on any hand-maintained list, which is what fleet has).
2. `schemagen.TestGenerateWholeTree`'s `EXPORTED_IDENTIFIERS.lock` assertions plus `mergeLock`'s existing stale
   check (prove claim 2).
3. `schemagen.TestGenerateReasonsScopedToSchema` (two synthetic status items declaring one shared
   code with different prose; asserts both declarations survive with their schema paths, and that
   the shared code's constant documents the divergence) (proves claims 3 and 6; would fail on a
   `map[string]Entry` keyed by code, which is the shape fleet and go-adm imply).
4. `ddm.TestConformanceReasons`, `status.TestConformanceReasons` (generated) (prove claim 4).
5. `apns.TestReasonVocabulary` (32 constants, all present in `Reasons`, none incomplete,
   `ReasonCodes()` sorted, an undocumented reason absent) (proves claim 5).
6. `apns.TestInvalidTokenReasonsArriveAsBadRequest` (pins that `BadDeviceToken` and
   `DeviceTokenNotForTopic` are documented as 400, which is *why* `pushOne` inspects the reason at
   all, and that the 410 reasons need no special case).

Failing paths: `schemagen.TestGenerateReasonsScopedToSchema` covers the two-declaration branch of
`reasonDescriptions`; `TestGenerateWholeTree` covers the no-reasons branch of `reasonsFile`.

## Rejected alternatives

- **A defined string type (`type ReasonCode string`) for the generated constants.** Every other wire
  identifier the generator emits (`RequestType*`, `MessageType*`, `DeclarationType*`) is an untyped
  string constant, and the field they are compared against — `ddmproto.StatusReportStatusReason.Code`
  — is a plain `string`. A defined type here alone would force a conversion at every use and give the
  codebase two conventions for the same idea.
- **One flat vocabulary shared across `schema/ddm` and `schema/status`.** It would contradict
  `statusreason.yaml` outright, and it would put names in a package whose schemas do not declare
  them, breaking the generator's invariant that a package is exactly its family's schemas.
- **Emitting `IsError`/`IsInfo` helpers from the `Error.`/`Info.` prefix.** The split is visible in
  the data but Apple states no rule anywhere in the schema or the documentation, so a helper would be
  our inference wearing generated clothing. A caller that wants to count only failures can filter on
  the constants it names.
- **A hand-written list of the 24 codes.** go-adm and fleet are the two outcomes: parse the block and
  forget it, or hand-write the two codes you happen to need. Both drift from Apple by construction.
- **Putting the APNs constants in `push`.** The vocabulary is APNs's, and `push` is the
  transport-agnostic interface; `push/apns` is the package that speaks the protocol. `push.Result`
  keeps the `Reason` field and gains no dependency.
- **Using the new constants in `apns.TestStatusMapping`.** Its literals independently pin the wire
  values; if the code under test and the test both read the same constant, a wrong constant value
  would pass.
- **Changing what `Invalid` means while here.** Record 0007 deliberately treats 400 `BadDeviceToken`
  and `DeviceTokenNotForTopic` as invalid tokens, where fleet has a test asserting `BadDeviceToken`
  must *not* turn MDM off and nanomdm classifies neither. Apple's own text for `BadDeviceToken`
  ("verify that the request contains a valid token **and that the token matches the environment**")
  means a production/sandbox mix-up presents as a fleet-wide invalid-token event, and our DDM
  notifier treats an `Invalid` result as delivered rather than retrying (`ddm/notifier.go:310`). That
  is worth revisiting on its own evidence, not inside a change that only renames literals.
  **Done in record 0042**, which found the citation behind 0007's claim does not support it.
