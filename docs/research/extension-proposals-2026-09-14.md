# Extension proposals — scope review, 14 September 2026

This is a review of the original eighteen proposals, not approval to implement
those features. IDs are preserved so that each disposition can be traced to the
original draft. **Maintenance is approved scope; retained feature candidates need
an individual design and the acceptance evidence below before adoption.**

## The boundary used for this review

The [README](../../README.md) and
[architecture decision 0001](decisions/0001-architecture.md) define a reusable Apple
device-management library and a reference server. Protocol codecs, Apple service
clients, storage contracts, narrowly specified helpers, diagnostics and executable
examples fit. Fleet inventory, compliance policy, generic workflow execution,
targeting and a multitenant management product do not. Putting a policy engine in
`server/` or behind an interface does not make it part of this charter.

The root module owns reusable protocol code and contracts. The server module owns
SQL implementations, HTTP/admin adapters and reference composition. New root
packages must not import the server module. Consumers retain decisions about whom
to target, what to enforce, when to retry a business operation, and how to operate
an organization. See the [module boundary](../architecture.md#modules-and-dependency-direction).

Documentation corrections, examples that match the implementation, generated
reference verification, and repairs to broken or redundant pipelines are in scope.
They precede new features because inaccurate descriptions currently make existing
capabilities look like missing infrastructure.

## Evidence baseline and corrections

Code was reviewed at `80ea482` (the main-branch application code on 14 September
2026). The subsequent `c2770c2` release commit changes release metadata, not that
code. Local links below identify implementation evidence; Apple schema links refer
to the vendored input pinned at `b0180185a5e4077070710033341b71d0cbe1a18a`
(`seed_OS_27_0`, 2 September 2026). The historical schema retains earlier contracts.
These are seed contracts, not a claim of validation on every released device OS.

Three assertions in the draft need correction:

- Legacy software-update commands are removed **on OS 27**. Earlier OS contracts
  remain supported. There is no `softwareupdates` evidence package in this checkout;
  an unmerged package needs an identified revision before it can be a dependency.
- Status querying, capability-derived subscriptions, certificate renewal, push
  topic guards and a durable event outbox already exist. They are not foundations
  that must first be built for this proposal list.
- `ProfileAssetReference` and new network declarations add declarative options.
  They do not establish that every legacy profile has been removed. Availability,
  deprecation and removal must be checked per payload, key, platform and OS version.

The [Apple device-management update guide](https://support.apple.com/guide/deployment/device-management-updates-depd638aa061/web)
and [pinned schemas](../../third_party/device-management) support version-specific
claims. The original unpinned survey cannot support “every product”, “absent from
open source” or “primary inventory feed” assertions. Those claims are removed.
Other implementations can motivate a use case; they cannot establish this
project's scope or Apple's protocol requirements.

## Dispositions

| ID | Decision | Candidate that remains |
|---|---|---|
| P1 | Narrow | Typed access to existing observations |
| P2 | Narrow | Explicit-target update declaration helpers |
| P3 | Split and narrow | Independent Apple secret codecs and sealed-storage gaps |
| P4 | Remove | Documentation of existing replacement/renewal only |
| P5 | Narrow | Validated declaration and asset composition |
| P6 | Narrow | Setup protocol helpers and an explicit example |
| P7 | Remove | Consumer orchestration examples only |
| P8 | Split and narrow | Trusted offline migration conversion and protocol examples |
| P9 | Already implemented; correct docs | Verify existing delivery guarantees |
| P10 | Mostly implemented; narrow | Redacted status-versus-push diagnostics |
| P11 | Retain narrowly | Apps and Books client and shared manifest helper |
| P12 | Retain with corrected key model | Managed Apple Account JWT helper |
| P13 | Retain narrowly | File-oriented profile lint using existing validation |
| P14 | Defer | A separately justified provider adapter, if needed |
| P15 | Retain narrowly | Missing admin query surfaces and a true read-only preview |
| P16 | Fold into P15/P17 | Diagnostics, examples and missing contract cases |
| P17 | Retain after coverage inventory | Missing simulator behavior and sanitized fixtures |
| P18 | Remove | Existing enrollment-resource authorization remains |

### P1. Typed access to observations, not an inventory product

**Present state and evidence.**
[`storage.DeviceInfo`](../../devicemanagement/storage/storage.go) already stores ten
fields populated from tracked acknowledgments.
[`ddm/status_query.go`](../../devicemanagement/mdmprotocol/ddm/status_query.go)
already offers prefix-filtered, paginated values, status errors and report history.
[`ddm/status.go`](../../devicemanagement/mdmprotocol/ddm/status.go) handles full and
partial reports; generated status types already supply the wire shapes.

**Decision and argument.** Keep a small typed observation adapter if it removes
repeated decoding for consumers. Remove the new inventory database, universal fact
model, compliance evaluator and inventory change feed. Those would introduce
product semantics and duplicate existing storage before demonstrating a gap.
Push discrepancies belong to P10; P4 is not a justification for P1.

**Retained boundary.** Decode selected known status values or command responses
with source, channel and observation time. Reuse existing queries and generated
types. An absent report is not proof that a setting is false or compliant.

**Acceptance before adoption.** Tests must distinguish full replacement from partial
updates, omitted from explicit null, device from user channel, and unknown fields
from invalid known fields. Preserve raw unknown values and provenance. Demonstrate
a caller that becomes simpler without adding a second authoritative device store.

### P2. Explicit software-update declaration helpers

**Present state and evidence.** Generated declarations and status types, the
[GDMF client](../../devicemanagement/appleplatformservices/gdmf), and the
[ADE software-update gate](../../devicemanagement/mdmprotocol/enroll/ade/softwareupdate.go)
already exist. The draft's in-progress package is not present. The pinned
[enforcement schema](../../third_party/device-management/declarative/declarations/configurations/softwareupdate.enforcement.specific.yaml)
defines target version/build and local deadline semantics.

**Decision and argument.** Keep pure helpers that build an explicitly selected
update declaration and interpret its reported progress. Remove rings, shards,
“latest minus N days”, sliding deadlines and an enforcement controller. Selecting
an organization's rollout policy belongs to the consumer.

**Retained boundary.** Validate the caller's target version/build and
`TargetLocalDateTime`; it is a local date-time without a timezone offset. GDMF
availability is evidence of a published asset, not proof of device readiness or
installation success. Settings and beta fields must use their own version gates.

**Acceptance before adoption.** Round-trip generated types, invalid/missing target
cases, local deadline cases, supported and removed contracts, and pending/failure
status examples. Preserve pre-27 command support. Any proposed admin deadline view
must describe advertised intent separately from device-reported state and reuse
P15 instead of creating an update-policy service.

### P3. Separate protocol codecs from secret lifecycle policy

**Present state and evidence.** Bootstrap and unlock tokens are sealed; raw command
queues and results are also sealed in persistent stores. Generated commands already
cover FileVault, Activation Lock and Recovery Lock. The
[FileVault rotation schema](../../third_party/device-management/mdm/commands/rotate.file.vault.key.yaml)
and [admin-password schema](../../third_party/device-management/mdm/commands/set.auto.admin.password.yaml)
constrain what helpers may promise.

**Decision and argument.** Split this into independently reviewable candidates:
FileVault CMS decoding, Activation Lock representations, and the
`SALTED-SHA512-PBKDF2` account-password hash format. Add sealed storage only for a
specific Apple escrow artifact not already represented. Remove reveal-triggered
rotation, rotation schedules, static per-fleet passwords, break-glass workflows and
an all-purpose escrow subsystem. These are policy and secret-management products.

**Retained boundary.** `SetAutoAdminPassword` targets the GUID of an administrator
created by ADE `AccountConfiguration`; it is not arbitrary local-account LAPS.
FileVault ciphertext must be decoded against the appropriate certificate/private
key, which must remain available across retry and delayed response. Do not impose
a fresh per-attempt certificate or delivery-time command subtype as a prerequisite.

**Acceptance before adoption.** Apple-format fixtures and independent cryptographic
vectors, malformed CMS and wrong-key cases, account GUID restrictions, bounded
hash parameters, secret-safe errors/projections, and persistence round trips for any
new stored value. Recovery Lock sequencing remains caller-owned and requires its
own design if a concrete gap is found.

### P4. Remove the desired-state reconciler

**Present state and evidence.** The queue already implements command delivery and
retry behavior. Controlled profile replacement and
[automatic identity renewal](../../server/internal/app/renewidentities.go) already
exist; renewal is triggered within sixty days of expiry. DDM has its own membership
[`Resolver` and `Expander`](../../devicemanagement/mdmprotocol/ddm/membership.go).

**Decision and argument.** Remove this proposal. Labels, shards, dependencies,
verification states, reapply-on-build-change and secret expansion together form a
fleet policy engine. Moving it into a library package would not change that.
A server-side preparation failure must not be fabricated as a device's MDM error
response.

**Maintenance acceptance.** Document the actual renewal/replacement behavior and
extension seams. If a consumer demonstrates a missing queue primitive, review that
primitive separately with protocol evidence; do not reintroduce this controller as
a dependency of other retained candidates.

### P5. Focus declaration composition on actual gaps

**Present state and evidence.** Generated declarations, predicate validation and
[capability-derived subscriptions](../../devicemanagement/mdmprotocol/ddm/subscriptions.go)
already exist. The [legacy configuration schema](../../third_party/device-management/declarative/declarations/configurations/legacy.yaml)
allows `ProfileURL` or `ProfileAssetReference` under their respective contracts.

**Decision and argument.** Retain helpers for declaration/asset references and
cross-document validation where generated single-object validation cannot express
the relationship. Remove duplicate subscription synthesis, a second predicate
validator, generic canary rollout and `plan/apply` deployment management.

**Retained boundary.** Validate profile and credential-asset references, identifier
forms and supported `app.settings` combinations against the pinned schema. Report
PPPC requirements only where documented. Profile takeover must preserve required
profile/payload identifiers, UUIDs, counts and ordering, with the documented MDM and
declaration-payload exclusions. Do not require CMS-signed profile assets unless the
specific Apple contract supports that representation. Keep older-OS delivery paths.

**Acceptance before adoption.** Valid/invalid reference graphs, missing or wrong
asset types, version-gated keys, takeover identity fixtures and generated-type
round trips. Treat the supported predicate subset explicitly; do not imply every
Apple predicate expression is evaluated by the server.

### P6. Setup protocol helpers and an example

**Present state and evidence.** ADE, `MachineInfo`, authenticated web views,
`AwaitingConfiguration`, `AccountConfiguration` and `DeviceConfigured` are already
represented. The [enterprise-install command](../../third_party/device-management/mdm/commands/application.install.enterprise.yaml)
acknowledges before package download/installation and does not later return an
installation-error command response.

**Decision and argument.** Keep an example that deliberately holds and releases
Setup Assistant using existing primitives, plus specific missing helpers. Remove
the setup workflow product, signed-package hosting route, default hidden admin and
automatic release on timeout. An acknowledgment is insufficient evidence to
release a device that must finish an installation first.

**Retained boundary.** Share the account hash codec with P3 and manifest helper with
P11. Let the caller decide prerequisites, installation evidence, failure handling
and explicit release. Keep migration-specific ordering in the migration example.

**Acceptance before adoption.** Awaiting and non-awaiting states, duplicate or late
messages, failure before completion, and an explicit `DeviceConfigured` decision.
The example must state which observations establish readiness and which merely
acknowledge command receipt. No dependency on P7.

### P7. Remove the generic workflow engine

**Present state and evidence.** Hooks, events, UUID-associated command results and
DDM notifications already permit callers to coordinate exchanges. The
[bench decision](decisions/0048-reference-server-bench.md) uses ordinary Go scenarios
and explicitly avoids a separate workflow language.

**Decision and argument.** Remove persistent steps, scheduling, exclusivity,
workflow context and event-triggered starts. They introduce a new execution model,
recovery contract and policy surface unrelated to implementing an Apple protocol.
The fact that consumers need orchestration does not require the library to own it.

**Maintenance acceptance.** Show bounded consumer examples through existing APIs
and tests where the protocol interaction is otherwise unclear. Keep bench scenarios
as test/example code, not a production scheduler.

### P8. Split migration compatibility from enrollment policy

**Present state and evidence.** Native-format export/import, replacement policy,
Return to Service and Apple Business migration calls already exist.
[Migration decision 0017](decisions/0017-enrollment-export-import.md) defines the import
boundary. `ShouldRetryEnrollment`, language and region are already represented and
have tests; they are not new implementation items.

**Decision and argument.** Retain a separately designed, offline converter for
trusted NanoMDM records into the existing migration contract, and examples of
Apple's native MDM migration sequence. Remove a permissive raw-check-in import HTTP
route, retroactive certificate trust changes, blocked-device lists, quota tokens,
label assignment and a general `Reenroll` policy operation.

**Retained boundary.** Source formats need pinned fixtures, explicit identity/topic
checks and a report of unsupported fields. Do not claim NanoMDM conversion also
covers every MicroMDM/Fleet deployment. Preserve the existing exclusions for command
queues, account associations and revocation state. Apple's MDM migration and Mac
Migration Assistant are different features; a generated Migration Assistant setting
does not implement MDM migration.

**Acceptance before adoption.** Idempotent import, collision/rejection cases, no
silent certificate repinning and a pushability check that does not send an
unauthorized wake. The native migration example must follow
[Apple's migration requirements](https://support.apple.com/guide/deployment/migrate-managed-devices-dep4acb2aa44/web):
Await Device Configured, required app licensing/reinstallation before release,
conditional Activation Lock handling and documented platform/exclusion rules.
Do not promise preservation for Shared iPad or Return to Service outside those
rules. Live-device evidence must be separate from simulated success.

### P9. The durable event outbox already exists

**Present state and evidence.** [`server/eventstore`](../../server/eventstore)
persists projected `event_records` and destination-specific `event_deliveries`, with
leases, retry, terminal failures and manual retry. Its publisher coordinates local
SQL mutations and event capture in one transaction through `event.Run`. The
[reference composition](../../server/internal/app/eventstore.go) wires this whenever
it uses SQL. [`adminevents.go`](../../server/internal/app/adminevents.go) and
[`dmctl events`](../../server/internal/dmctl/eventverbs.go) already expose inspection
and retry.

**Decision and argument.** Remove “build an outbox” from the feature backlog and
correct the stale bus-only documentation now. Kafka, NATS, Splunk, CloudEvents and
label filters require individual demonstrated use cases; they are not needed to
make the existing implementation durable.

**Actual boundary.** Destinations are captured with each event. This is not
arbitrary historical replay into newly configured sinks. Webhooks/custom external
sinks are at least once and need EventID deduplication; native audit append and
acknowledgment share a transaction when using the same SQL pool. Slog and in-memory
bus subscribers remain ephemeral. Transactional event-capture failure can roll
back a participating local operation; the old universal “persistence errors never
fail device operations” statement is wrong. Denial records are captured separately
after rollback, and failure to capture one cannot authorize the request.

**Maintenance acceptance.** Document inspection, retry, destination changes,
retention boundaries and failure guarantees; exercise existing restart, lease,
rollback, redaction and destination-isolation tests on supported SQL backends.
See the [event-delivery guide](../operations/event-delivery.md).

### P10. Narrow push work to discrepancy diagnostics

**Present state and evidence.** Push coalescing, failure classification, invalid-token
events and certificate version rechecks exist. The
[certificate lifecycle implementation](../../devicemanagement/pki/lifecycle/certificates.go)
already rejects a renewed push certificate with a different topic. Push certificate
validation and enrollment setup also enforce topic identity.

**Decision and argument.** Remove the duplicate topic guard and silent-enrollment
sweep worker. A periodic wake policy needs consumer-defined expectations about
activity and is not an APNs protocol requirement. Retain a diagnostic comparing
reported `mdm.push-token`/`mdm.push-magic` with stored routing state if a concrete
troubleshooting case warrants it.

**Acceptance before adoption.** Missing, stale, malformed and channel-specific
reports must not silently overwrite authoritative push state. Return a bounded,
redacted mismatch result with source/time; never emit raw token or magic values to
logs/events. Confirm existing topic-mismatch tests before adding coverage.

### P11. Apps and Books client; no app policy engine

**Present state and evidence.** Commands/declarations and Apple Business app/package
listings exist. The [Apple Business Get Apps documentation](https://developer.apple.com/documentation/applebusinessapi/get-apps)
explicitly distinguishes built-in management from external MDM and directs external
MDM developers to Apps and Books. It does not establish that these listing endpoints
replace external-MDM licensing.

**Decision and argument.** Retain a protocol client for the documented Apps and Books
management API in `appleplatformservices`, plus an independent manifest helper. This
fills an Apple-service client gap within the charter. Remove install polling policy,
a package lifecycle controller and declarative app reconciliation.

**Retained boundary.** Follow [management API setup](https://developer.apple.com/documentation/devicemanagement/getting-started-with-the-management-api)
and [service configuration](https://developer.apple.com/documentation/devicemanagement/service-config):
location server tokens, service discovery/configuration, asset/assignment operations
and authenticated notifications as documented. Do not invent a token-exchange flow
or hard-code a legacy service solely from another implementation. Token custody and
licensing decisions stay with the caller. A licence-before-install example may
compose this with existing commands.

**Acceptance before adoption.** Pinned request/response fixtures, token/location
isolation, pagination, rate-limit/retry and notification authentication cases. The
manifest helper needs fixtures matching [Apple's asset contract](https://developer.apple.com/documentation/devicemanagement/manifesturl/itemsitem/assetsitem),
including chunk sizes/hashes and malformed package input; it must not add hosting.
P6 consumes the same helper.

### P12. Managed Apple Account token helper with the right key

**Present state and evidence.** [`GetTokenHandler`](../../server/service/service.go)
already documents the `com.apple.maid` claims and delegates policy to the caller.
The [DEP client](../../devicemanagement/appleplatformservices/dep) exposes account
details; the [AXM client](../../devicemanagement/appleplatformservices/axm) uses a
different authentication key model.

**Decision and argument.** Retain a small RS256 JWT helper. Correct the draft's
ambiguous “stored DEP or ABM key”: the RSA private key must correspond to the MDM
server certificate registered with Apple, with `AccountDetail.server_uuid` as
issuer. The AXM API's ES256/P-256 credentials are not interchangeable.

**Acceptance before adoption.** An independently verified signature, exact
handler-documented claims, UTF-8 TokenData encoding, issuer, issued-at clock cases, and
rejection of wrong key types. Leave the decision to answer/refuse with the caller.
A bench refusal case must identify the applicable account-driven enrollment mode;
refusal must not be documented as universally causing self-unenrollment.
`watch.enrollment` remains a distinct handler/contract.

### P13. File-oriented profile lint

**Present state and evidence.** [`profile`](../../devicemanagement/mdmprotocol/profile)
already parses and validates profiles, and [`dmctl`](../../server/internal/dmctl)
already explains registered profile schemas. The gap is convenient validation of
an arbitrary `.mobileconfig` with useful file/key diagnostics.

**Decision and argument.** Retain a thin command over those existing facilities.
Do not create another schema registry, parser or automatic profile-to-declaration
converter. This improves the usability of existing protocol support.

**Acceptance before adoption.** Unsigned and supported signed input, nested payload
errors, unknown payloads preserved as unvalidated, platform/version-specific
support and distinct deprecation/removal diagnostics. Declaration alternatives must
cite a real Apple mapping with its limitations; do not infer equivalence merely
from similar names. Include exit-status and human-readable error examples.

### P14. Defer vendor CA adapters until a concrete gap exists

**Present state and evidence.** [`ca.Signer`](../../devicemanagement/pki/ca/ca.go)
already abstracts signing, including external implementations. The
[SCEP client](../../devicemanagement/pki/scep/client.go) already enrolls and renews.
The draft's “local abstraction” and missing “generic SCEP” premise is incorrect.

**Decision and argument.** Defer NDES/step-ca adapters. First distinguish a
server-side signer from a device profile that enrolls directly with an external
SCEP service. Those have different authentication, challenge and renewal contracts.
[Smallstep provisioner documentation](https://smallstep.com/docs/step-ca/provisioners/)
is provider-specific evidence, not a universal challenge-URL convention.

**Acceptance to reopen.** Name one supported provider/version, its missing operation,
credential ownership and an integration fixture. Prove it cannot be composed with
the current signer/client. Do not impose one-time challenges in profile URLs,
subject conventions or CertificateList-driven renewal on all CAs.

### P15. Complete DDM inspection without mutating delivery state

**Present state and evidence.** The DDM engine already queries values, errors and
reverse-chronological reports. Admin tooling already exposes status, values, tokens
and stored declarations. The current values route is capped at the first 1,000
values; library pagination/error/history capabilities are not all exposed.
The engine's `Manifest`, `Tokens` and `DeclarationItems` refresh persisted snapshots.

**Decision and argument.** Retain paginated admin/CLI error and history access and a
true read-only per-enrollment preview. Reusing the current delivery methods unchanged
would violate the proposed read-only contract. Share this surface with P1/P10/P16.

**Retained boundary.** Distinguish the last advertised snapshot from a computation
of current intent. A preview must not save snapshots, advance tokens/change rows or
send notifications. Apply existing resource authorization and redact credentials in
expanded declaration/asset data.

**Acceptance before adoption.** Multi-page queries with stable order/cursors,
unknown enrollment, device/user channels, scoped authorization, secret redaction
and before/after store assertions proving that preview has no write or push side
effects. State clearly when the device's observed state is unavailable.

### P16. Fold log and Lost Mode work into diagnostics and tests

**Present state and evidence.** Enhanced-log and Lost Mode command types already
exist, and service tests exercise seeded enhanced-log contracts. Lost Mode predates
OS 27; neither its novelty nor absence from all surveyed projects is established.
The [pinned command schemas](../../third_party/device-management/mdm/commands)
define platform, supervision and state restrictions.

**Decision and argument.** Remove this as a standalone feature. Add missing status
visibility through P15 and specific examples/behavior tests through P17. An AppleCare
logging token is supplied by Apple and collection uploads to Apple; this is not a
general-purpose log-upload host. Do not add a location inventory feed or an automatic
Lost Mode sequence with potentially disruptive state changes.

**Acceptance for folded work.** Exercise unsupported/unsupervised cases, reported
errors and cancellation; distinguish command acknowledgment from collection
completion. Redact logging tokens and location data from ordinary output. Require
explicit caller actions for enable, locate, sound and disable.

### P17. Add only missing protocol behavior coverage

**Present state and evidence.** Generated conformance and seeded service tests
already cover OS 27 fields, mandatory software-update/PSSO gates and retry behavior.
The [bench catalogue](../testing/bench-catalogue.md) records executable scenarios;
field presence and a successful simulator exchange do not prove physical-device
compatibility.

**Decision and argument.** Retain a coverage audit followed by named missing cases
for watch pairing, tvOS, visionOS and new status behavior. Do not duplicate generated
field tests or market this as universal platform support. Watch enrollment is
configured on the paired iPhone (iOS 17+), not by directly enrolling the watch with
the iPhone profile.

**Acceptance before adoption.** Map each new case to an Apple contract and an
existing test gap, including wrong platform/channel, invalid prerequisites and
retry/error behavior. A transcript recorder is optional separate work: scrub
identities, tokens, credentials, private keys and location before fixtures enter
version control. Record OS/build and provenance. Keep simulated, replayed and live
results visibly separate and preserve dated live-device evidence.

### P18. Remove labels-as-tenancy

**Present state and evidence.** Named Apple credentials and enrollment-resource
Cedar authorization already exist in
[`adminauthz.go`](../../server/internal/app/adminauthz.go). They do not establish
organization-wide isolation for every store or bulk operation.

**Decision and argument.** Remove targeting labels and tenant scoping from this
backlog. A label attribute alone cannot isolate declarations, bulk queries, events,
credentials, migrations and all other state. This would expand the project into a
multitenant management product without designing that product's boundaries.

**Maintenance acceptance.** Describe the existing scoped authorization accurately.
Use explicit enrollment-resource policy examples; never describe them as full
tenant isolation. Consumers needing tenancy must provide and validate it outside
this project's promised contract.

## Implementation phases

### Phase 1 — documentation and evidence repair (maintenance)

Rewrite this review; align README, architecture, package docs, operational guides,
ADRs and affected diagrams with existing code. In particular, explain durable event
capture versus ephemeral bus delivery, existing DDM queries and existing renewal.
Preserve dated research/live-device records; add corrections to current guidance
instead of rewriting past observations. Preserve unrelated working-tree edits.

Acceptance: every P1–P18 has a disposition and evidence; current links/commands
resolve; docs describe present behavior rather than retained proposals. Verify
changed diagrams from their JSON sources with artifact and browser evidence.

### Phase 2 — pipeline and test repairs (maintenance)

The [14 September main run](https://github.com/deploymenttheory/go-apple-dm/actions/runs/34856317612)
shows a Linux supervisor-stop race and transient Go proxy HTTP/2 download failures
in Windows candidate installation and PostgreSQL E2E setup. The latter is not a
PostgreSQL behavior failure. The [server release run](https://github.com/deploymenttheory/go-apple-dm/actions/runs/34856662154)
shows a native Windows startup timeout followed by a held workspace lock during
failed-test cleanup. Release assets were consequently skipped.

Repair control-response shutdown and failed-test cleanup; allow measured startup
headroom while retaining deadlines. Keep standard Go dependency commands and cache
behavior; transient proxy failures can be rerun at the failed-job level. A new
download script or cross-workflow retry layer is not justified by these two failures.
Keep checksum/authentication/module errors and build/test failures fatal. Verify
generated output once and detect stale
as well as changed/missing files and removed locked public names.

Remove the repeated SQLite-only embedded acceptance catalogue from the PostgreSQL
E2E invocation. Retain backend-specific E2E and separate process acceptance. Avoid
rebuilding release archives for changelog-only updates, while retaining executable,
dependency, packaging and relevant workflow triggers. Document the
[CI responsibility matrix](../testing/ci.md). Similar-looking checks over candidate
modules, published modules and packaged binaries cover different inputs and remain.

Acceptance: workflow lint/security and behavioral script tests; targeted race tests;
schema verification; normal cross-platform CI including native Windows; SQL contract
and backend E2E suites, process acceptance and the unchanged coverage gate. Validate
future release gates without rewriting tags or republishing an existing release.

### Phase 3 — independently designed feature candidates (not implemented here)

Start with P13/P15's bounded tooling gaps and P12's corrected signing helper, then
P11's Apple-service client. P1/P2/P3/P5 require small protocol-focused designs and
fixtures. P6/P8 are examples or compatibility helpers with explicit trust/ordering
contracts. P10/P16 feed shared diagnostics; P17 supplies identified coverage gaps.
P14 remains deferred. P4/P7/P18 are removed; P9 is maintenance of existing code.

No retained item depends on a new inventory store, workflow engine, reconciler or
label model. Each future implementation must update its API/package documentation,
examples, operations guidance and applicable generated references/diagrams in the
same phase, with evidence for its acceptance criteria. Review any change to this
boundary as an explicit charter change rather than slipping it into a helper.
