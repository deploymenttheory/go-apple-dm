# 0036: `dmctl explain` over `schema/support`

Status: accepted
Date: 2026-09-02
Phase: 8

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/commands-and-queries>
- Doc: <https://developer.apple.com/documentation/devicemanagement/profile-specific-payload-keys>
- Doc: <https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations>
- Doc: <https://developer.apple.com/documentation/devicemanagement/status-items>
- Doc: <https://github.com/apple/device-management/blob/release/docs/schema.md> (`supportedOS`, the
  per-key metadata every answer here comes from)
- YAML: `third_party/device-management/docs/schema.yaml` (the meta-schema defining `supportedOS`,
  `sharedipad`, `userenrollment`, `allowed-enrollments`, `allowed-scopes`, `accessrights`)
- YAML: every family under `third_party/device-management/{mdm,declarative,other}/**`, which the
  generator turns into the `schema/support` tables this command reads

## References read

- Ours: `schema/support/support.go` (the shipped API), `schema/commands/registry.gen.go`,
  `schema/profiles/registry.gen.go`, `schema/ddm/registry.gen.go`, `schema/status/registry.gen.go`,
  `internal/schemagen/emit_support.go`, `schema/validation/validation.go`
- `macadmins/contour` (Rust) — schema-driven validation of profiles and declarations against an
  embedded copy of Apple's schema; the closest prior art to answering "does this key apply here",
  and the only tool in the survey that consults `supportedOS` at all
- `korylprince/go-adm` `yamlschema`, `declgen` — generates types from the same YAML but keeps no
  runtime support metadata
- `jessepeterson/mdmcommands` — generated command structs with no support metadata
- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a` `server/mdm/apple/` — hardcoded OS
  version comparisons at the call sites rather than a queryable table
- `docs/research/reference_projects.md` section 4 (the codegen survey)

## Known pitfalls found

- `docs/research/implementation_plan.md` (before this record) specified
  `Supports(key Path, os OS, v Version, ch Channel, c Context) Support` and
  `Removed(key Path) []Removal`. **Neither exists.** Phase 1 shipped `(*Entry).Check(Target) Result`
  with `Lookup`, `Register`, `Families` and `Paths`, and the plan text was never corrected — so the
  plan of record described an API for seven phases that nothing implemented.
- `schema/support/support.go:193-194`: `Check` returns `Supported: true` with the reason
  `"no support data"` when the entry is nil, and `Supported: true, Reason: "no target OS"` when the
  target has no OS. Rendering either as "supported" would make the tool assert facts Apple never
  stated. Verified by reading the two early returns.
- `schema/profiles/registry.gen.go`: **six** distinct Go types report the same `PayloadType`
  `com.apple.MCX` (`Accounts`, `EnergySaver`, `FDEFileVaultOptions`, `MobileAccounts`, `TimeServer`,
  `WiFiManagedSettings`), which is why the generated `ByID` returns a slice. Verified by count.
- The generated `Entry` carries `ID`, `Schema`, `Title` and `New` — but **not** the Go type name,
  and the Go type name is exactly what roots a support path (`commands.Support("DeviceLock.Message")`).
  So `ByID` alone cannot resolve a wire id to its support entry; the registry map key is needed.
- The generated packages carry no per-key prose. `Entry.Title` is per type, not per key, and Apple's
  descriptions live only in the YAML, so any "description" this command printed would be invented.
- `OSSupport`'s booleans are `*bool` on purpose: nil means Apple did not state the value. Collapsing
  nil to false would silently claim, for example, that a key is not allowed on the user channel when
  Apple simply did not say.
- Support families register in `init()`, so `support.Families()` returns only what the binary
  imports; a command that imports five families silently cannot answer about the other three.

## What they do

- **contour**: embeds Apple's schema in the binary and validates a supplied profile or declaration
  against it, reporting unknown keys and type mismatches. It answers "is this document valid",
  not "where does this key apply", and has no per-OS target query.
- **go-adm, mdmcommands**: generate Go types from the same YAML and discard `supportedOS`
  entirely, so nothing downstream can ask the question.
- **Fleet**: compares OS versions inline at the call sites that care (software update, ADE gating),
  with no queryable table, so the knowledge is scattered and untestable as a unit.
- **Nobody** ships a command that answers "does `DeviceLock.PIN` apply to a supervised Mac on
  15.0, and if not, why not".

## What we do better

1. The plan of record is corrected rather than worked around. This record documents that
   `Supports`/`Removed` were never built and amends `implementation_plan.md` to describe the shipped
   `Check`/`Lookup`/`Families`/`Paths` API, so the plan and the code agree for the first time since
   phase 1.
2. A near miss is answered with the name the operator meant. Suggestions try a case-insensitive
   substring and fall back to a shared prefix, ranked closest first: a dropped letter makes
   `DeviceLok` no substring of `DeviceLock`, so substring matching alone answers a typo with
   silence, and alphabetical ordering buries the intended name under its siblings.
3. An ambiguous identifier produces every answer, never a guess. `explain com.apple.MCX` prints all
   six blocks, each headed by its Go type name, `Title` and schema path, sorted by registry key, and
   exits 0 — a complete answer to an ambiguous question is a success, not an error. Resolution scans
   the registry map rather than calling `ByID`, because the map key is the type name that roots the
   support path and `Entry` does not carry it.
4. "Apple did not say" is rendered as itself. A nil tri-state prints `-`, never `no`; an empty
   `Mode` prints `-`; `NotAvailable` collapses the row to `n/a`. The distinction between "forbidden"
   and "unstated" is preserved end to end, which is the whole reason the generator kept `*bool`.
5. The two "we don't know" answers are not "OK". `Check`'s `"no support data"` and `"no target OS"`
   print as `unknown`, so the command never reports a key as supported on the strength of missing
   metadata.
6. Nothing is invented. Output is derivable from `Title`, `Schema`, the support path and
   `Result.Reason` alone; there is no per-key prose in the generated code, so the command cites
   `third_party/device-management/<Schema>` instead of paraphrasing Apple. It does not parse the
   YAML: a second schema reader outside `internal/schemagen` would drift from the generator.
7. `Result.Reason` is printed verbatim, so `explain` and the server agree byte for byte — the same
   string appears in the `ErrUnsupportedTarget` that `Core.Enqueue` returns and that `POST /commands`
   reports in `Skipped`. An operator who asks why a command was refused and an operator who asks in
   advance get the same sentence.
8. It is offline. `explain` builds no client and reads neither `-server` nor `-token`, so it works
   against a laptop with no deployment, and it imports all eight families so no question is silently
   unanswerable.

## Verified by

1. `explain.TestShippedAPIMatchesThePlan` (asserts the families and entry-point functions this
   record names exist), and the amended `docs/research/implementation_plan.md` (proves claim 1; the
   previous text named two functions that no package exported).
2. `explain.TestSuggest/ClosestFirst`, `/PrefixFallbackCatchesADroppedLetter`, `/NoNonsenseMatches`
   (prove claim 2; a substring-only search returns nothing for `DeviceLok`, and an alphabetical
   ordering puts `DeviceConfigured` above `DeviceLock`).
3. `explain.TestResolve/AmbiguousProfilePayloadTypeListsSixBlocks`, `/ByGoTypeName`, `/ByWireID`,
   `/ByDottedPath` (prove claim 3; would fail on any implementation using `ByID` alone, which loses
   the type name and cannot build the support path).
4. `explain.TestTriStateNilPrintsDash` (proves claim 4; would fail on a renderer that treats `*bool`
   nil as false, which is what a flat non-pointer support struct — the shape the plan originally
   described — would have forced).
5. `explain.TestNoSupportDataIsNotOK` (proves claim 5; it would report `OK` if the renderer trusted
   `Result.Supported` alone).
6. `explain.TestNoDescriptionsAreInvented` (every output line is derivable from `Title`, `Schema`,
   `Path` or a `Reason`) (proves claim 6).
7. `explain.TestTargetReasonIsVerbatim` (asserts the rendered output contains `Check`'s reason
   unchanged) (proves claim 7).
8. `dmctl.TestExplainNeedsNoServer` (runs with an unreachable `-server` and succeeds),
   `explain.TestFamiliesAndListings` (all eight families registered) (prove claim 8).

Failing paths: `explain.TestParseTarget/BadOS`, `/BadVersion`, `/UnknownWord`,
`explain.TestResolve/EmptyArgument`.

## Rejected alternatives

- Adding `Supports`/`Removed` to `schema/support` to match the old plan text: the shipped
  `Check(Target) Result` is strictly more expressive — it answers with a reason and a deprecation
  flag rather than a bare verdict — and inventing the promised signatures now would mean two ways to
  ask the same question. The plan is corrected instead.
- Parsing the YAML to print Apple's key descriptions: a second reader of the schema outside
  `internal/schemagen` would drift from the generator, and the submodule may not be checked out.
  The schema path is cited so the reader can look.
- Teaching the generator to emit descriptions: worth doing, but it changes every generated package
  and `EXPORTED_IDENTIFIERS.lock`, so it belongs with a generator change rather than a CLI one.
- Printing `OK` when there is no support data: it would make the command confidently wrong exactly
  where Apple is silent, which is the failure mode that makes support tables untrustworthy.
- Picking the first match for an ambiguous id: six real payload types share `com.apple.MCX`, and
  choosing one silently would answer a question the operator did not ask. `-first` exists for
  scripts that want it explicitly.
- Evaluating the target server-side and asking the API: `explain` must work with no deployment, and
  the answer is a pure function of generated tables compiled into the binary.
