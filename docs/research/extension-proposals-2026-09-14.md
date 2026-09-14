# Feature implementation plan

Implement the tasks below in order. All listed tasks fit the project's library
and reference-server scope. Task IDs belong to this plan; the earlier proposal
numbers are retired. All tasks start **not implemented**; mark them complete only
after their acceptance checks pass.

## Execution rules

- Put reusable codecs, helpers and Apple clients in `devicemanagement/`.
  Put HTTP/admin adapters, CLI commands and SQL implementations in `server/`.
  Root-module code and tests must remain independent of the server module.
- Extend existing parsers, generated types, queries and storage contracts.
  Check the current checkout before adding an API; reuse equivalent work and
  record its tests instead of implementing it twice.
- Keep target selection, rollout timing, account policy and operational decisions
  in caller code. Helpers take explicit inputs and return protocol data or results.
- Use the pinned `third_party/device-management` schemas and examples for wire
  contracts. For external APIs, record the official documentation URL, retrieval
  date and sanitized fixtures. Resolve undocumented fields before implementing
  the affected operation; never fill a protocol gap by guessing.
- Preserve platform, OS-version, supervision and device/user-channel restrictions.
  Keep supported older-OS paths. Change generated output through the generator.
- Reuse existing authorization, secret sealing and event redaction. Diagnostic
  output must exclude credentials, tokens, recovery keys and location data.
- Complete one numbered task with its tests and documentation before moving on.
  Record changed APIs, validation commands and remaining limitations in its PR.
  Preserve unrelated working-tree edits.

The documentation and CI repairs in [PR #57](https://github.com/deploymenttheory/go-apple-dm/pull/57)
are the baseline. Existing event delivery, identity renewal, subscriptions and
migration storage are dependencies to reuse. The
[architecture decision](decisions/0001-architecture.md) defines module ownership.

| Phase | Deliverable | Dependencies |
|---|---|---|
| 1 | Profile lint and DDM inspection | Existing profile, DDM and admin APIs |
| 2 | Reusable protocol helpers and diagnostics | 1.2 for diagnostic integration |
| 3 | App manifest helper and Apps and Books client | Existing Apple-client conventions |
| 4 | Setup and migration tools/examples | Dependencies specified per task |
| 5 | Missing simulator and bench behavior | Features exercised by each scenario |
| 6 | Documentation and CI completion | All implemented tasks |

## Phase 1 — inspection tools

### 1.1 Profile file lint

**Work in:** [profile](../../devicemanagement/mdmprotocol/profile),
[schema support](../../devicemanagement/schema/support) and
[dmctl](../../server/internal/dmctl).

- Add an offline `dmctl` command that reads a `.mobileconfig`, calls
  `profile.Parse` and validates it for an explicit target.
- Report the file, payload index/type and failing key. Distinguish invalid,
  unsupported, deprecated and removed fields. Mark unknown or ambiguous payload
  types as unvalidated; preserve their content.
- Handle unsigned and supported CMS-signed input through existing parse options.
  Report signature verification and certificate trust separately.

**Done when:** CLI tests cover valid, malformed, signed, nested-invalid, unknown
and version-gated profiles, with documented exit codes and example output.

### 1.2 Paginated DDM status queries

**Work in:** [status queries](../../devicemanagement/mdmprotocol/ddm/status_query.go),
[admin wiring](../../server/internal/app/admin.go) and
[dmctl](../../server/internal/dmctl).

- Expose `StatusValues`, `StatusErrors` and `StatusReports` through authenticated
  admin routes and typed CLI commands. Replace the values route's fixed
  `Limit: 1000` with the existing paging contract and prefix filter.
- Preserve device/user enrollment identity, ordering and cursors. Apply existing
  enrollment-resource authorization and safe projections to retained reports.
- Keep existing route consumers compatible; add fields or routes deliberately
  and document any unavoidable compatibility change.

**Done when:** tests traverse more than 1,000 records without loss/duplication and
cover empty results, invalid cursors, unknown enrollments, both channels,
unauthorized access and secret redaction.

### 1.3 Read-only DDM preview

**Depends on:** 1.2.

**Work in:** [DDM engine](../../devicemanagement/mdmprotocol/ddm),
[server admin](../../server/internal/app) and [dmctl](../../server/internal/dmctl).

- Add a read-only path for computing an enrollment's current declaration intent.
  Reuse membership, expansion and token calculation without persisting a snapshot.
- Return the stored delivery snapshot, computed intent and available reported
  state as distinct results. A stored snapshot does not prove device receipt.
- Expose the result through the admin API and CLI with authorization and redaction.
  Existing `Manifest`, `Tokens` and `DeclarationItems` refresh snapshots;
  calling them unchanged does not meet this task's contract.

**Done when:** before/after assertions show no snapshot, token timestamp, change
row or notification writes. Cover membership changes, missing observations,
both channels, authorization and credential-bearing declarations.

## Phase 2 — protocol helpers

### 2.1 Managed Apple Account JWT

**Work in:** a small helper under
[enrollment](../../devicemanagement/mdmprotocol/enroll), integrated through
[GetTokenHandler](../../server/service/service.go).

- Build UTF-8 JWT TokenData for `com.apple.maid` with RS256.
- Take an RSA signing key matching the registered MDM server certificate,
  `AccountDetail.server_uuid` as issuer, issued-at time and unique JWT ID.
  Emit the handler-documented claims, including `service_type`.
- Add a handler example where caller code chooses whether to issue the token.
  Keep AXM ES256 credentials and `watch.enrollment` on their existing contracts.

**Done when:** independent signature verification, exact claims/encoding,
invalid-key and clock tests pass; the example covers issuance and refusal for
its specified enrollment mode.

### 2.2 ADE account password hash

**Work in:** [ADE helpers](../../devicemanagement/mdmprotocol/enroll/ade).

- Implement the `SALTED-SHA512-PBKDF2` binary-plist representation from the
  [password-hash schema](../../third_party/device-management/other/passwordhash.yaml).
- Accept the password and explicit bounded derivation parameters; generate salt
  with cryptographic randomness. Reuse the existing plist and generated types.
- Show use with `AccountConfiguration` and `SetAutoAdminPassword`; the latter
  requires the GUID of the administrator created through ADE.

**Done when:** independent derivation vectors and decoded plist fixtures match,
invalid parameters fail safely, and errors/output contain no password material.

### 2.3 FileVault recovery-key decoding

**Work in:** a focused helper under
[mdmprotocol](../../devicemanagement/mdmprotocol), reusing
[CMS](../../devicemanagement/mdmprotocol/cms).

- Decode `RotateResult.EncryptedNewRecoveryKey` using the matching reply
  certificate/private key and the
  [rotation contract](../../third_party/device-management/mdm/commands/rotate.file.vault.key.yaml).
- Return the decoded result to the caller. Document retaining the matching key
  across retries and delayed replies; reuse existing sealed command/result storage.

**Done when:** independent CMS fixtures cover successful decoding, malformed
envelopes, unsupported algorithms, wrong keys and delayed-response key selection.
Errors and diagnostic projections must never contain the recovery key.

### 2.4 Activation Lock bypass-code helper

**Work in:** a focused helper under
[mdmprotocol](../../devicemanagement/mdmprotocol).

- Implement the missing bypass-code representation/conversion operations from
  Apple's “Creating and using bypass codes” contract. Record the exact supported
  format and official source beside independent fixtures before writing the codec.
- Reuse generated Activation Lock request/response types and their target checks.
  Keep retrieved codes and caller-generated codes distinguishable; preserve opaque
  device-returned values where Apple specifies no conversion.

**Done when:** independent vectors verify each supported representation, malformed
input fails safely, and command examples preserve platform/channel restrictions
and redact code values. Command execution remains an explicit caller action.

### 2.5 Typed status observations

**Work in:** [DDM status queries](../../devicemanagement/mdmprotocol/ddm/status_query.go)
and [generated status types](../../devicemanagement/schema/status).

- Add typed accessors for software-update progress/failure and MDM push status,
  the values consumed by 2.6 and 2.7. Follow the existing `ClientCapabilities`
  accessor pattern; read the current status store.
- Return source path, enrollment/channel, observation time and decoding outcome.
  Preserve unknown values and distinguish absent, explicit null and malformed data.
- Add a consumer example using the accessors without creating another device store.

**Done when:** tests cover full replacement, partial updates, null/omitted fields,
unknown fields, invalid known values and channel isolation.

### 2.6 Push-state discrepancy diagnostics

**Depends on:** 1.2 and 2.5.

**Work in:** [reference-server admin](../../server/internal/app) and
[dmctl](../../server/internal/dmctl).

- Compare reported `mdm.push-token`/`mdm.push-magic` observations with stored
  routing state for one explicitly selected enrollment and channel.
- Return match, mismatch, missing or invalid observations with timestamps.
  Let callers interpret age using those timestamps; comparisons never update
  routing state or send a wake.
- Expose only redacted comparison results through existing diagnostic surfaces.

**Done when:** missing, old, malformed and cross-channel cases are covered;
store/push assertions prove no side effects and output contains no token or magic.

### 2.7 Explicit software-update declarations

**Depends on:** 2.5 for progress interpretation.

**Work in:** [DDM helpers](../../devicemanagement/mdmprotocol/ddm), reusing
[GDMF](../../devicemanagement/appleplatformservices/gdmf) and generated declarations.

- Build and validate an enforcement declaration from a caller-selected target
  version/build and `TargetLocalDateTime`.
- Validate the deadline as a local date-time without a timezone offset.
  Apply the pinned enforcement/settings schemas' field and version gates.
- Interpret progress/failure through 2.5. Keep selected intent, published asset
  availability and device-reported installation state distinct.

**Done when:** generated-type round trips, invalid targets/deadlines, support
gates and pending/failure observations pass. Existing pre-27 command tests stay green.

### 2.8 Declaration and asset composition

**Work in:** [DDM helpers](../../devicemanagement/mdmprotocol/ddm), using the pinned
`legacy`, credential-asset and `app.settings` declaration schemas.

- Add cross-document validation for profile/credential references, identifiers,
  required asset types and supported combinations. Reuse single-object validation,
  predicates and subscription synthesis.
- Add profile-takeover composition fixtures preserving required profile/payload
  identifiers, UUIDs, counts and ordering, including documented exclusions.
- Validate target-specific `ProfileURL`/`ProfileAssetReference` and asset
  representations against the pinned contracts.

**Done when:** valid graphs, dangling/wrong-type references, unsupported keys,
takeover identity and round-trip tests pass, with a small composition example.

## Phase 3 — app distribution primitives

### 3.1 App/package manifest helper

**Work in:** a reusable package under
[mdmprotocol](../../devicemanagement/mdmprotocol).

- Build and validate Apple's documented enterprise-install manifest from explicit
  package metadata and asset URLs, using existing schema types where available.
- Implement required size, chunk and hash calculations for the supported format.
  Record the official manifest contract and sanitized fixtures with the helper.
- Keep package hosting and installation decisions with the caller.

**Done when:** independent manifest fixtures, chunk boundaries, hash/size mismatch,
malformed input and bounded-reader tests pass; a command-building example compiles.

### 3.2 Apps and Books client

**Work in:** a new package under
[appleplatformservices](../../devicemanagement/appleplatformservices), following
existing client and test-server conventions.

- Implement documented service configuration/discovery, assets and assignment
  operations using location server tokens.
- Implement the documented notification verification contract and typed payloads.
  Use caller-supplied credentials, context, HTTP transport and retry settings.
- Add a simulated service and a licence-before-install example using existing
  install commands. AXM built-in-management app listings remain a separate API.

**Done when:** official request/response fixtures cover pagination, location/token
isolation, rate limits, retry/cancellation, API errors and invalid notifications.
The example obtains licensing success before issuing an install command.

## Phase 4 — setup and migration

### 4.1 Explicit ADE setup example

**Depends on:** 2.2 and 3.1; use 3.2 when the example installs licensed apps.

**Work in:** [server examples/tests](../../server/e2e) and
[ADE helpers](../../devicemanagement/mdmprotocol/enroll/ade).

- Compose existing enrollment, `AwaitingConfiguration`, account configuration,
  app commands and `DeviceConfigured` in ordinary Go.
- Make prerequisites, installation observations and the release decision explicit
  inputs. An install-command acknowledgment alone does not establish installation.
- Add only helpers needed to express this exchange through existing APIs.

**Done when:** scenarios cover awaiting/non-awaiting state, duplicates, late
messages, installation failure and deliberate release. The guide names the
observations required to proceed and explains how callers handle failure.

### 4.2 Offline NanoMDM conversion

**Work in:** a converter over
[EnrollmentExport](../../devicemanagement/storage), wired to a local
[dmctl](../../server/internal/dmctl) operation.

- Pin one NanoMDM revision and source export format. Convert trusted offline
  records to the existing migration format with a mapping/unsupported-field report.
- Validate identity, parent/channel relationships, certificate pins and push topic.
  Output device records before their user records.
- Use existing import validation and target-store sealing. Preserve the exclusions
  in [decision 0017](decisions/0017-enrollment-export-import.md).
  Conversion itself performs no import or APNs request.

**Done when:** sanitized fixtures cover idempotent conversion/import, disabled
devices, collisions, missing parents, mismatched topics and certificate conflicts.
The guide describes protected handling of exported secrets and a pre-import check.

### 4.3 Native Apple MDM migration example

**Depends on:** 4.1 and 3.2 for setup/licensing; use 2.4 where bypass-code conversion
is required. This is independent of the offline converter.

**Work in:** [server E2E](../../server/e2e) and
[AXM](../../devicemanagement/appleplatformservices/axm) examples.

- Compose existing Apple migration calls and enrollment primitives for a stated
  platform/OS and enrollment mode.
- Follow Apple's documented ordering for Await Device Configured, app licensing
  and reinstallation, conditional Activation Lock handling and explicit release.
- Document eligibility, preserved state, exclusions and recovery from interruption.

**Done when:** scenarios cover the supported sequence, invalid prerequisites and
interruption. Record simulator evidence separately from any physical-device results.

## Phase 5 — simulator and bench coverage

**Work in:** [simulator](../../devicemanagement/simulator),
[bench](../../server/internal/bench) and [E2E](../../server/e2e).

- Map watch pairing, tvOS, visionOS, new status behavior, enhanced logging and
  Lost Mode to existing generated, service and scenario tests. List only missing
  behavioral exchanges in the bench catalogue, with their Apple contract.
- Implement each identified gap, including wrong platform/channel, supervision,
  invalid prerequisites and applicable retry/error/cancellation paths.
- Keep watch pairing on its documented paired-iPhone enrollment flow. Model
  logging acknowledgment separately from completion. Lost Mode enable, locate,
  sound and disable remain individually initiated actions.
- Use sanitized fixtures with source revision and OS/build. Label results as
  simulated, replayed or live; preserve existing dated live records.

**Done when:** each added scenario closes a named gap, appears in the executable
catalogue and passes deterministically. Shared scenarios pass embedded/process
adapters where applicable; fixtures and output contain no sensitive material.

## Phase 6 — documentation and CI completion

Documentation belongs in each task's change. Finish with a repository-wide
consistency pass and validation of the combined implementation.

- Update README capabilities, package docs, CLI help, admin API examples,
  operations guides and applicable ADRs to describe the implemented behavior.
  Update affected diagram sources and regenerate/validate their artifacts.
- Verify examples, local links, target-version statements and the bench catalogue.
  Keep unimplemented work in this plan rather than capability documentation.
- Repair broken workflows and reproducible test failures. Remove overlapping steps
  only when their inputs and tested contracts are equivalent; update the
  [CI responsibility matrix](../testing/ci.md).
- Retain normal Go dependency resolution and existing caches. Keep the current
  platform/backend matrix, checksum verification and coverage requirements.

**Done when:** affected race-enabled tests and both module linters pass;
`make verify` passes; workflow changes pass `actionlint`; full CI passes native
Linux/macOS/Windows tests, SQL contracts, both E2E backends, process acceptance
and the coverage gate. Run applicable package/release checks without publishing.

Record test commands/results, documentation changes and any unverified live-device
claims in the final implementation review. Mark completed tasks in this file.
