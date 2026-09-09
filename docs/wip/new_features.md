# Library feature roadmap: enabling Apple device management servers

Status: proposal, not a release commitment. Baseline: `main` at `a7e770e` (v0.2.0),
Apple schema pin v26.4. Prepared 2026-09-09.

This roadmap is for library maintainers and engineers building an Apple device management
server with these packages. It identifies additions and refinements to the reusable library,
including reusable services, adapters and SQL stores in the `server` module. It does not
specify the future opinionated server.

**Research basis — 2026-09-09.** Apple links describe the protocol; implementation links
point to inspected source files or tests at fixed commits. Vendor behaviour is evidence to
consider, not an Apple requirement or a recommendation to copy code. Fleet's NanoMDM fork
shares ancestry with upstream NanoMDM and is not independent corroboration.

The review covers the named files and examples, not every feature of every vendor. “Not
found” means not found in those inspected sources. Upstream tests were read, not executed;
no hardware testing was performed for this document. Current Apple pages and vendor snapshots
may describe features beyond the library's v26.4 pin. Preserve the pin and investigate
differences explicitly. “Possible approach” records a design suggestion; “Unresolved
questions” records evidence still needed.

## Ownership boundary

> The library owns Apple-defined wire formats, validation and protocol state machines,
> together with storage contracts and mechanisms that servers would otherwise implement
> identically. Products own policy, workflow, hosting, presentation and scheduling.
> The library enables a server; it does not become one.

For every addition, name the exact Apple rule or unavoidable protocol bookkeeping that a
consumer would otherwise reproduce. Making one server easier to build is insufficient.
Reusability, configurable inputs and storage interfaces do not establish library ownership.
An interface that defines an authoritative inventory record can prescribe product behaviour
as firmly as an implementation.

The library exposes protocol evidence and explicit operations. The future server decides
how to combine that evidence into its model of a device and what action to take. Protocol
state required to authenticate a request, correlate a response or serve a declaration
version belongs here. A consolidated account of what should be installed, what is currently
installed or whether a device meets policy belongs to the product.

| Library responsibility | Product responsibility |
|---|---|
| Extract enrollment facts from specific messages and evaluate supplied facts against Apple requirements | Maintain current enrollment facts, resolve conflicting sources and decide admission |
| Encode a selected command or declaration and validate its references | Choose its recipients, content, rollout and approval process |
| Interpret `NotNow`, preserve command identity and persist exchange state | Choose queue order, retry eligibility, delays, expiry and push cadence |
| Read a software catalog and encode an explicit update declaration | Choose versions, assemble update workflows and define compliance |
| Associate licences through Apple's API and interpret the results | Choose assignments and coordinate installation or removal |
| Decode individual reports and explain their scope, omissions and source | Combine reports into current inventory and choose freshness, retention and views |
| Verify signatures and apply supplied trust configuration | Choose trusted issuers, identity providers and access policy |
| Validate artifact metadata and supplied bytes | Store, publish, authorize and distribute artifacts |
| Supply component APIs, store migrations and assembly examples | Choose application structure and service composition |

Preserve the existing [module and tier boundaries](../research/decisions/0044-repository-layout.md).
The root module must not import `server`. Schema packages must not import storage or runtime
wrappers that already depend on those schemas. Shared models belong at the lowest layer
that can express them without reversing dependencies.

The [architecture guide](../architecture.md) describes implemented capabilities. Entries
below distinguish maintenance work, confirmed gaps, bounded extensions and research that
must precede an interoperability claim. They also identify deferred work that requires
evidence from the future server. Proposed names are descriptive, not reserved public APIs.

## Foundation priorities

### F1. Protocol coverage and evidence register

**Problem to solve.** As a library maintainer, I want to know which Apple protocol features
are implemented and what evidence supports them, so that I can identify gaps when Apple
changes the schema and publish accurate support information.

**Baseline — maintenance work.** The generator emits types, validators, support metadata and
provenance. The service already has a
[registry-to-dispatch check-in test](../../server/service/checkincoverage_test.go).
Neither generated coverage nor dispatch alone establishes working device behaviour.

**Research context.**

- **Apple documentation.** [Apple's schema guide](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/docs/schema.md) defines
  inherited platform support and per-key overrides. Coverage must therefore account for
  key-level changes as well as new command names. The schema is a source for wire and
  support metadata, not evidence that a service dispatches or correctly executes an exchange.
- **Other implementations.** [go-adm](https://github.com/korylprince/go-adm/blob/7a87c98afb418bebb6c3f94b9edacf634ce55a2c/README.md) separates
  schema generators from runtime payload tools; its README still lists schema-based
  validation as future work. [admgen](https://github.com/jessepeterson/admgen/blob/b26a7609b0a326988e3cea8853de5ee0061f8fab/cmd/admgencmd/builder.go)
  generates response `Validate` methods that inspect status/error chains. That method
  name alone does not establish full field or platform validation.
- **Possible approach.** Keep separate coverage dimensions for generated representation,
  semantic checks, runtime handling and device evidence. Use the other generators to
  identify schema interpretation differences and examples worth testing against this pin.
- **Unresolved questions.** No equivalent end-to-end coverage register was found in these
  inspected generator sources. Inspecting generated output or a vendor's feature list cannot
  establish physical-device coverage. This remains maintenance work rather than a new API.

**Addition.** Maintain coverage by protocol family with separate evidence for generated
representation, decoding/encoding, runtime handling where applicable, semantic validation,
simulation and physical-device testing. Record prose-only Apple constraints and known
limitations. Report changes between schema pins: added or removed definitions, changed
fields and changed platform support. Keep source revision and evidence links with the report.

**Boundary.** This is repository engineering and documentation work, with no new runtime
service, fleet report or public coverage API. Data-only definitions need appropriate codec
and validation evidence rather than service handlers. Third-party examples are
interoperability evidence, not the protocol authority.

**Acceptance.** A schema change appears in a reviewable coverage report. A newly generated
type cannot silently become “fully supported.” Unsupported or unverified behaviour has a
specific explanation and an investigation or implementation entry.

**Dependencies.** None. Establish this first and extend it with every later feature.

### F2. Enrollment facts and support assessment

**Problem to solve.** As a device management developer, I want to extract enrollment facts
from Apple messages and check the facts I supply against Apple's support requirements, so
that I can identify unsupported operations and missing information without inventing those
protocol rules myself.

**Baseline — confirmed gap.** The [service target conversion](../../server/service/service.go)
assumes supervision, DEP and user approval. Stored enrollment data cannot currently justify
those assumptions. `support.Target` uses booleans, so unknown facts also need an explicit
compatibility design.

**Research context.**

- **Apple documentation.** The pinned [SecurityInfo schema](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/mdm/commands/information.security.yaml)
  distinguishes enrollment fields and their platform availability; the
  [DeviceInformation schema](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/mdm/commands/information.device.yaml) supplies
  `IsSupervised`. A supported command does not imply every response key is supported on
  every platform. Extract field presence independently of its boolean value.
- **Other implementations.** Zentral's
  [SecurityInfo handler](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/zentral/contrib/mdm/commands/security_info.py)
  copies management flags only when their response values are booleans. Its
  [DeviceInformation handler](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/zentral/contrib/mdm/commands/device_information.py)
  updates supervision when the key is present. The same handlers save device records and
  perform product actions; those portions do not justify a shared facts database.
  [SecurityInfo tests](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/tests/mdm/test_security_info_command.py)
  provide examples of empty responses, explicit false values and differing iOS/macOS data.
- **Possible approach.** Use these cases to design extraction tests, while returning values
  and source fields to the caller. Keep the decision to update a current device record
  outside the extractor.
- **Unresolved questions.** These implementations do not establish a universal precedence
  between enrollment route, stored inventory and device reports. Hardware captures are still
  needed for omitted keys under different access rights and enrollment methods.

**Addition.** Extract supervision, user approval and enrollment mode from individual typed
responses, preserving absent versus false values and the source field. Describe enrollment
route evidence only where the transport and documented method establish it. Expose support
checks over caller-supplied facts, with findings for unmet requirements and unknown inputs.
Any conversion from stored records belongs above the schema layer.

Replace undocumented assumptions in service target assessment with explicit inputs or
clearly named compatibility behaviour. Preserve existing boolean callers through a
documented transition; do not silently treat their false values as unknown.

**Boundary.** The server owns the authoritative facts record, source precedence, conflict
resolution, freshness, operator overrides and refresh timing. A general `FactStore`, automatic
result-to-facts persistence and facts export are deferred. A future persistence addition must
identify protocol-required state that existing enrollment/result stores cannot represent.
Support checks report findings; the product decides whether an unknown finding blocks work.

**Acceptance.** Explicit false supervision produces an unmet-requirement finding; an omitted
key remains unknown. Extraction leaves other messages and stored records untouched. The same
explicit facts produce the same assessment. Fixtures respect platform availability: the
pinned schema identifies `EnrolledViaDEP` and `UserApprovedEnrollment` as macOS-only and
`IsSupervised` as available on macOS from 10.15. Existing callers have documented compatibility
coverage. No new facts database is required to use these functions.

**Dependencies.** F1 for sources; F5 for findings. Independent of a new facts store or
composition package.

### F3. User-channel authentication interoperability

**Problem to solve.** As a device management developer, I want Mac user-channel requests to
be authenticated according to Apple's actual protocol, so that legitimate users can connect
and my service can reject requests that do not satisfy those authentication requirements.

**Baseline — research gate.** The existing user-authentication exchange stores a token. The
optional gate checks stored handshake state, not token possession on the incoming request.
[Decision 0016](../research/decisions/0016-user-authenticate-state.md) records the limitation.

**Research context.**

- **Apple documentation.** [User Authenticate](https://developer.apple.com/documentation/devicemanagement/user-authenticate?changes=_4)
  specifies an empty challenge for token-free user management, HTTP 200 with an empty token
  after an invalid password, and HTTP 410 when declining management. It describes token
  use at both endpoints and validity until the next `UserAuthenticate` request. These rules
  also appear in the [pinned schema notes](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/mdm/checkin/userauthenticate.yaml);
  the token lifecycle is partly documented already.
- **Other implementations.** [NanoMDM](https://github.com/micromdm/nanomdm/blob/d61174ca4e386674067cc99a6b93215f86dc88f4/service/nanomdm/ua.go)
  implements optional empty-digest handling or refusal. The
  [Fleet fork](https://github.com/fleetdm/fleet/blob/5e5976b269c8aa86c34d6ddd64024bee39437a23/server/mdm/nanomdm/service/nanomdm/ua.go)
  retains that approach; it is related code, not independent confirmation of token handling.
  [MicroMDM](https://github.com/micromdm/micromdm/blob/904493b9500ffc8a21846846781e362f5c612107/mdm/checkin.go) rejects this message, while
  [Zentral's check-in dispatcher](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/zentral/contrib/mdm/public_views/mdm.py)
  returns 410 and marks network/mobile-user management as unfinished.
- **Possible approach.** Implement the documented response distinctions and token lifetime
  explicitly. Treat an expiring digest challenge separately from the resulting authentication
  token. Use the empty-digest implementations as examples of that branch only.
- **Unresolved questions.** No subsequent token-carrier verification was found in these
  inspected handlers. They do not establish a default header, single-use tokens or a
  command-endpoint exemption. Capture a real nonempty-challenge exchange before claiming
  those details work.

**Addition.** Establish the token carrier and verify channel binding using Apple documentation
and physical macOS exchanges. Implement the documented handshake responses and token lifetime,
covering both check-in and command endpoints:
[Apple describes subsequent token presentation to both URLs](https://developer.apple.com/documentation/devicemanagement/user-authenticate?changes=_4).
Then add adapter extraction, verification and protocol-required state transitions. Use
constant-time secret comparison and atomic storage operations for transitions that can race.
Expose distinguishable internal errors without requiring detailed authentication failures
to be disclosed on the wire.

**Boundary.** Do not invent single-use semantics, expiration requirements or an exemption
for later command traffic. Products retain identity-provider, trust and admission decisions.
Preserve the existing narrower handshake guarantee through documented compatibility settings
until replacement behaviour is supported by evidence.

**Acceptance.** Sanitized captures and fixtures cover correct, missing and incorrect tokens,
binding to the wrong device or channel, repeated presentation and the next handshake.
Tests distinguish challenge expiry from authentication-token validity. An invalid-password
response and a refusal to manage the user follow Apple's different response rules. No
default carrier is presented as validated before the investigation establishes it.

**Dependencies.** Investigation starts immediately. Extend existing user-authentication
storage contracts for confirmed transitions; use F13 for any required migrations.

### F4. Command state-machine and persistence contracts

**Problem to solve.** As a device management developer, I want the library to keep each
command and response correctly associated through reconnects, repeated messages and service
restarts, so that I can recover protocol exchanges without corrupting their recorded state.

**Baseline — extension and boundary correction.** The queue already models delivery,
acknowledgement, errors and `NotNow`, with deduplication and backend contract tests.
[Core storage](../../storage/storage.go) also embeds a particular exponential `NotNow`
backoff, which is a scheduling choice.

**Research context.**

- **Apple documentation.** [Sending MDM commands](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device?changes=latest_minor)
  describes cached results after disconnects, duplicate status messages and correlation
  through `CommandUUID`. It also warns that internal error codes/domains may change.
  [NotNow handling](https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses?changes=_4)
  requires resending the command if it is retried: the device does not retain the command
  for later execution. Automatic polling depends on which status it last sent.
- **Other implementations.** [NanoMDM's queue contract](https://github.com/micromdm/nanomdm/blob/d61174ca4e386674067cc99a6b93215f86dc88f4/storage/queue.go)
  separates report storage from retrieval with `skipNotNow`. Its
  [KV implementation](https://github.com/micromdm/nanomdm/blob/d61174ca4e386674067cc99a6b93215f86dc88f4/storage/kv/queue.go) groups status/raw-result
  storage and queue unlinking in a transaction and retains `NotNow` entries.
  [Zentral's managed-app command](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/zentral/contrib/mdm/commands/managed_application_list.py)
  illustrates the product layer: it applies its own retry growth and schedules follow-up
  requests while updating installation records.
- **Possible approach.** Turn duplicate reports, cached-result replay and interrupted writes
  into backend contract scenarios. Expose scheduling decisions separately. Preserve original
  errors and use version-qualified evidence for any higher-level classification.
- **Unresolved questions.** Transactional code is not proof of every race or restart guarantee.
  The inspected implementations need targeted adversarial tests before adopting a concurrency
  technique. No universal retry interval follows from Apple's exchange rules.

**Addition.** Audit and strengthen duplicate-result handling, command correlation, terminal
state protection, repeated delivery, concurrent polling and recovery after interruption.
Separate Apple-required transitions from command selection and caller-supplied retry
eligibility. Keep atomic storage operations for recording delivery/results and protecting
correlation; identify which guarantees come from Apple and which are necessary for a
consistent implementation. Preserve the full Apple error chain. Add classifications only
where protocol evidence supports them, retaining unknown identifiers and details. Document
any OS-version dependence rather than treating internal error numbers as stable contracts.

**Boundary.** Products choose the next eligible command, retry timing, priority, expiry,
cancellation and push cadence. Existing queue conveniences can remain for compatibility,
but the exchange and store contracts must allow consumers to supply those decisions. This
work does not introduce a scheduler or a workload management framework. Wire behaviour follows
[Apple's `NotNow` rules](https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses?changes=_4).

**Acceptance.** Shared backend tests cover duplicate and late responses, concurrent requests,
failure injection and restart recovery. Retrying a storage operation cannot regress a
terminal state or correlate a result to another enrollment. Different caller-supplied retry
and selection decisions use the same protocol transitions. Document delivery guarantees
without promising exactly-once execution on the device.

**Dependencies.** None; F1 records the evidence and F13 carries backend changes.

## Authoring and interpretation priorities

### F5. Structured validation and generated authoring helpers

**Problem to solve.** As a device management developer, I want to create profiles and
declarations with clear feedback about invalid or unsupported settings, so that I can
correct mistakes before they reach devices and spend less time assembling Apple's formats
by hand.

**Baseline — extension.** Generated types and validators exist, as do runtime profile and
declaration envelopes. Construction is largely manual and validation is primarily exposed
as errors.

**Research context.**

- **Apple documentation.** [Common payload keys](https://developer.apple.com/documentation/devicemanagement/commonpayloadkeys)
  define identity, type and payload version; the
  [declaration envelope](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/declarative/declarations/declarationbase.yaml)
  separately defines identifier, type, token and content. The two envelopes should not be
  forced into an identical builder shape.
- **Other implementations.** [mdmcommands](https://github.com/jessepeterson/mdmcommands/blob/0ef71b4590d729da33dcb653c5f7d8e6bdce6624/shared.go)
  provides command constructors with an explicit caller UUID.
  [cfgprofiles](https://github.com/jessepeterson/cfgprofiles/blob/3214f542ad05ab3809c9c76b4ac991083b5126e3/payloads.go) centralizes common payload
  fields and creates UUIDs in its constructors. Both are examples of removing mechanical
  envelope work. [Contour](https://github.com/macadmins/contour/blob/430d758b8502a3bf007e7fbbe4c0e9622966aed9/crates/profile/src/validation/schema_validator.rs)
  returns issues with severity, payload index/type, field, message and code; it also has
  configurable checks and third-party identifier exceptions that require separate judgment.
- **Possible approach.** Combine explicit caller identities with generated envelope helpers
  and structured findings. Use Contour's issue separation as an interface example, without
  importing its validation defaults or warning-suppression policy.
- **Unresolved questions.** No inspected constructor establishes that fresh UUIDs are required
  for every update. These examples also do not provide a complete design for this library's
  introduced/deprecated/removed support findings; that must follow the pinned metadata.

**Addition.** Provide consistent findings with key paths, severity, schema provenance,
support ranges and available Apple descriptions. Generate construction helpers into a layer
that may legally import both schemas and runtime wrappers. Cover profiles and declarations,
including required envelope fields and explicit references between payloads.

Accept stable caller-supplied identifiers and make fresh identity generation explicit.
Keep authored content separate from the engine's server-token calculation. Reference
validation must follow the supplied document's completeness and Apple's rules, rather than
assuming every external reference is invalid.

**Boundary.** Builders encode explicit caller choices. Products select settings, choose
recipients and assemble resources into workflows. Add a convenience helper only when it
removes repeated envelope construction or a specific Apple rule; avoid a second API that
merely renames generated fields. Prose-only constraints need sources and focused tests.

**Acceptance.** A Wi-Fi and certificate profile can be authored concisely and round-trips
without semantic change. An unsupported key produces a finding with its path and introduced
version. Fixed identities and content produce stable output. Generation and exported-name
checks remain deterministic.

**Dependencies.** F1; coordinate support findings with F2 and graph checks with F8.

### F6. Profile and declaration inspection

**Problem to solve.** As a device management developer, I want functions that verify signed
profiles, explain schema fields and report differences between supplied configurations,
so that I can build review tools without duplicating signature checks or Apple metadata.

**Baseline — extension.** Profiles already support signing and validation. `dmctl explain`
already reads schema metadata. [Declaration token calculation](../../mdmprotocol/ddm/token.go)
and canonicalization already exist.

**Research context.**

- **Apple documentation.** [Apple Configurator's signing instructions](https://support.apple.com/en-au/guide/apple-configurator-mac/pmd85719196/mac)
  describe signing, unsigning before edits and signing again.
  [Payload identity rules](https://developer.apple.com/documentation/devicemanagement/commonpayloadkeys)
  guide correspondence during replacement. [DDM integration](https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management?changes=late_3)
  treats revision tokens as comparison values, leaving their generation format to the server.
- **Other implementations.** [Zentral's CMS utilities](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/zentral/contrib/mdm/crypto.py)
  separate payload-signature verification from certificate-store checks.
  [mobileconfig-signer](https://github.com/hslatman/mobileconfig-signer/blob/0f288ff6758026eb56f5bc597a64cb91ca943204/main.go) constructs attached
  PKCS#7 signatures; it is a signing example, not a verification implementation.
  [Contour's profile diff](https://github.com/macadmins/contour/blob/430d758b8502a3bf007e7fbbe4c0e9622966aed9/crates/profile/src/diff/profile_diff.rs)
  compares lines of serialized plist XML. Fleet's
  [profile verifier](https://github.com/fleetdm/fleet/blob/5e5976b269c8aa86c34d6ddd64024bee39437a23/server/mdm/apple/profile_verifier.go)
  instead compares expected and installed profiles with grace periods and retry handling.
  Its name does not imply CMS verification.
- **Possible approach.** Keep signature validity and caller-selected trust checks distinct.
  Build the proposed identity-aware per-key diff rather than treating a text diff or
  deployment verification service as an equivalent implementation.
- **Unresolved questions.** No equivalent identity-aware semantic diff was found in the
  inspected Contour, cfgprofiles and Fleet files. Define duplicate/missing identity findings
  and test signed attributes, tampering and trust failures independently. Preserve the
  library's current token algorithm; vendor token encodings are not Apple requirements.

**Addition.** Add signed-profile verification with explicit trust options, document-level
explanation and semantic diffs. Distinguish signature validity from signer trust. Compare
payload identities using the applicable profile semantics and report per-key differences.
Add a convenient declaration-level token operation while preserving the existing byte-level
API and canonicalization contract.

**Boundary.** A diff describes changes; products decide their impact, presentation, approval
and deployment. Trust verification uses caller-supplied trust configuration. Never require
blanket UUID regeneration: [Apple's replacement guidance](https://developer.apple.com/documentation/devicemanagement/commonpayloadkeys)
describes updates to matching payload identities. Inspection must not mutate those identities
or prescribe remove/reinstall actions. CLI examples demonstrate the functions.

**Acceptance.** Valid signatures verify; tampering and untrusted chains yield distinct
failures. A one-key edit produces the expected diff. Explanation identifies deprecated or
unsupported keys using available schema text. Equivalent declaration content retains the
engine's existing token.

**Dependencies.** F5 for findings and metadata; reuse existing CMS and canonical JSON code.

### F7. Forward-compatible decoding and predicate conformance

**Problem to solve.** As a device management developer, I want the library to preserve
unfamiliar data that Apple permits and clearly distinguish supported activation expressions
from ones it cannot evaluate, so that new device behaviour does not cause avoidable data
loss or misleading test results.

**Baseline — extension and research gate.** DDM status storage already preserves unfamiliar
values. Other codecs need a targeted round-trip audit. The predicate package deliberately
supports a subset, and activation upload validation currently depends on that subset.

**Research context.**

- **Apple documentation.** The [activation schema](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/declarative/declarations/activations/simple.yaml)
  refers to Apple's predicate language, and [WWDC22's DDM session](https://developer.apple.com/videos/play/wwdc2022/10046/)
  describes property/status references and device-side evaluation.
  [Predicate syntax](https://developer.apple.com/library/archive/documentation/Cocoa/Conceptual/Predicates/Articles/pSyntax.html)
  documents case/diacritic modifiers and ICU-style `MATCHES`. Its general language guide
  alone does not prove every construct works in a DDM activation.
- **Other implementations.** [KMFDDM's declaration parser](https://github.com/jessepeterson/kmfddm/blob/7f06151330d7827cc9b2b79332067f8d4fd3ceb4/ddm/declaration.go)
  retains raw JSON alongside a small envelope and exposes a separate basic validity check.
  [Zentral's linker](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/zentral/contrib/mdm/declarations/linkers.py)
  focuses on declaration structure/references; neither inspected path demonstrates a local
  predicate evaluator. The [macadmins test activation](https://github.com/macadmins/ddm_examples/blob/f2c23934b9ed5826b02c376eb3b50718b295b039/activations/io.macadmins.activation.test.json)
  supplies a concrete property-equality example. [ddm_infra](https://github.com/macadmins/ddm_infra/blob/7ae13840f84be72baaaceb29205d84048d63dbeb/README.md)
  supplies deployment setup, not evidence of grammar support.
- **Possible approach.** Preserve raw content where allowed and record separate parsing and
  evaluation expectations for each reference example. Start with expressions actually found
  in the inspected material; keep simulator limitations visible.
- **Unresolved questions.** No full evaluator or evidence for all proposed extended operators
  was found in those paths and examples. Missing-field round-trips and ICU/RE2 differences
  still need explicit tests. Do not widen accepted device syntax solely from community files.

**Addition.** Preserve unknown fields where the wire format permits them, without relaxing
required envelope checks or allowing duplicate/reserved fields to bypass validation.
Distinguish successful decoding from a claim of platform support.

Collect versioned predicate reference examples from Apple documentation, samples, repository
fixtures and attributed community examples. Separate syntax recognition, documented device support
and local evaluation capability. Add constructs according to that evidence. An expression
the simulator cannot faithfully evaluate must produce an explicit unsupported result;
different ICU and RE2 behaviour must not silently become simulated Apple behaviour.

**Boundary.** Lack of a sample is not proof that a construct is invalid. Local evaluation
primarily supports the simulator and is optional for consumers. It must not determine device
membership or replace device activation decisions. A local evaluation limitation alone must
not reject an otherwise documented expression. Fleet targeting and policy languages belong
to the server.

**Acceptance.** Permitted unknown data survives round-trips; malformed data fails clearly.
Reference examples record their sources and expected parse/evaluation outcomes. Supported cases,
unsupported cases and fuzz inputs have explicit results. Device-support claims cite
documentation or recorded hardware evidence.

**Dependencies.** F1. Predicate research can proceed alongside F2–F6.

### F8. DDM graph and capability diagnostics

**Problem to solve.** As a device management developer, I want to check supplied declaration
references and capabilities and identify which content a report refers to, so that my
diagnostic tools can explain protocol errors using the correct configuration version.

**Baseline — extension.** The engine already stores declarations and membership, creates
snapshots, parses client capabilities and queries status. This is not a proposal to rebuild
those mechanisms.

**Research context.**

- **Apple documentation.** The [simple activation](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/declarative/declarations/activations/simple.yaml)
  references configuration identifiers; the
  [capability status schema](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/declarative/status/management.client-capabilities.yaml)
  describes supported versions, features and payloads. Its changes have an implicit status
  subscription. These are protocol relationships rather than a fleet-targeting model.
- **Other implementations.** [Zentral's linkers](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/zentral/contrib/mdm/declarations/linkers.py)
  traverse schema reference metadata, including references nested in arrays. They also
  resolve application-specific `ztl:` references against database artifacts.
  [Linker tests](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/tests/mdm/test_declarations_linkers.py)
  cover nested references, type compatibility and missing artifacts.
  [Zentral's status interpreter](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/zentral/contrib/mdm/declarations/status_report.py)
  recovers artifact-version information using its own token format.
- **Possible approach.** Adapt schema-driven reference traversal into diagnostics over
  caller-supplied declarations. Use reported identifier/token pairs to correlate content
  through this library's snapshot contract. Keep Zentral's database resolution and token
  decoding out of that interface.
- **Unresolved questions.** The inspected linker tests do not establish complete capability
  matching or device activation behaviour. Define how incomplete collections and missing
  capabilities appear in findings. A reported token from another server must remain opaque.

**Addition.** Validate Apple-defined references, expected reference kinds and relationships
between activations, configurations and assets in a caller-supplied collection. Report
unresolved references and capability mismatches. Distinguish missing or malformed capability
evidence from an explicit lack of support. Expose outcomes alongside their reported tokens
and existing snapshot association so consumers can identify which content a report describes.

**Boundary.** Checks operate on supplied collections and capability evidence. Preserve the
distinction between a reported token and an association inferred from a stored snapshot;
return unknown when the evidence cannot identify content. Products own fleet diagnosis,
membership, repair, publication decisions and rollout. Apple's
[declarative data model](https://developer.apple.com/documentation/devicemanagement/leveraging-the-declarative-management-data-model-to-scale-devices)
defines the relationships being checked.

**Acceptance.** Wrong-kind and unresolved references produce precise findings. Incomplete
capability evidence remains unknown. Diagnostics preserve existing snapshot/token stability
and do not confuse an old declaration outcome with confirmation of new content.

**Dependencies.** F5 and F7 where predicate diagnostics are involved.

## Additional protocol capabilities

### F9. Apps and Books service client

**Problem to solve.** As a device management developer, I want to issue explicit licence
requests to Apple and read their pending, successful and failed results, so that I can use
Apple's API without writing its authentication, request encoding and response handling.

**Baseline — confirmed gap.** Apple service clients include DEP, GDMF and AXM; there is no
dedicated Apps and Books licence-management client.

**Research context.**

- **Apple documentation.** [The management API](https://developer.apple.com/documentation/devicemanagement/app-book-and-subscription-management?changes=lat_3_1_4_6)
  separates assets, users, asynchronous events and legacy operations.
  [Pagination guidance](https://developer.apple.com/documentation/devicemanagement/using-paginated-endpoints)
  explains changing `versionId` values and which endpoints accept `sinceVersionId`;
  asset listing does not support that incremental parameter.
  [Client configuration](https://developer.apple.com/documentation/devicemanagement/clientconfigrequest)
  specifies the notification bearer secret and MDM ownership information.
- **Other implementations.** [Fleet's client](https://github.com/fleetdm/fleet/blob/5e5976b269c8aa86c34d6ddd64024bee39437a23/server/mdm/apple/vpp/api.go)
  models association requests and event identifiers, validates device-versus-user targets,
  and also contains a legacy user-registration call.
  [Zentral's v2 client](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/zentral/contrib/mdm/apps_books.py)
  reads service-config metadata URLs, checks MDM identity and detects version changes during
  pagination. Its [tests](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/tests/mdm/test_apps_books_client.py)
  include token errors, missing event IDs and changing paginated data.
- **Possible approach.** Provide explicit request/result types, authenticated notification
  decoding and pagination metadata. Use the vendor tests as scenarios, while letting the
  caller decide how to recover from a changing dataset. Do not automatically adopt
  Zentral's decision to abort such an iteration.
- **Unresolved questions.** Fleet's mixed API surface is not evidence that every combination
  is supported for the same token; verify each operation against Apple's migration guidance.
  Review user-assignment and metadata coverage separately from device licences. Real tokens
  remain necessary to verify latency, notifications and error classification.

**Addition.** Add a client with explicit API-version coverage for service tokens, assets,
licence operations, registered users and metadata. Model asynchronous operation identifiers,
completion results, pagination, Apple errors and rate-limit information. Provide notification
decoding and authentication verification separately from hosting. Include a deterministic fake
with delayed completion and partial failures. Treat app and book capabilities separately
where Apple's rules differ.

The initial design must account for the
[asynchronous Apps and Books API](https://developer.apple.com/videos/play/wwdc2021/10137/),
rather than assume that a legacy synchronous association call represents all current behaviour.
Record any legacy compatibility surface explicitly.

**Boundary.** Products choose accounts, assignments, polling, recovery retries and notification
hosting. Parsing a retry hint or observing a completed Apple operation is a library concern;
choosing the business action to take next is not. Do not hide an unbounded polling loop inside
an apparently single-request method.

**Acceptance.** Fixtures verify encoding, pagination, authentication failures, partial results
and asynchronous completion. The fake can exercise callers without an Apple account. Real
token testing establishes interoperability separately from fake coverage.

**Dependencies.** F1 and existing Apple-client conventions. Independent of authoring work.

### F10. Application and package management primitives

**Problem to solve.** As a device management developer, I want to encode explicit app
operations, validate the files I supply and decode Apple's installation reports, so that I
can implement my product's deployment workflow using tested protocol functions.

**Baseline — extension.** Generated installation/removal commands and declarative app/package
types exist. Reusable construction, interpretation and artifact validation are incomplete.

**Research context.**

- **Apple documentation.** [Install Enterprise Application](https://developer.apple.com/documentation/devicemanagement/install-enterprise-application-command)
  says macOS acknowledges parameter validation before download/installation and does not
  return later installation errors through that command.
  [Installing packages](https://developer.apple.com/documentation/devicemanagement/installing-packages?changes=_3)
  and the [manifest schema](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/other/manifesturl.yaml) distinguish whole-file
  hashes from lists of hashes over data chunks.
- **Other implementations.** [Fleet's manifest helper](https://github.com/fleetdm/fleet/blob/5e5976b269c8aa86c34d6ddd64024bee39437a23/server/mdm/apple/appmanifest/appmanifest.go)
  accepts a reader and URL, computes a SHA-256 hash and serializes a manifest.
  Its [tests](https://github.com/fleetdm/fleet/blob/5e5976b269c8aa86c34d6ddd64024bee39437a23/server/mdm/apple/appmanifest/appmanifest_test.go)
  cover a short input and reader failure. Zentral's
  [managed-app response handler](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/zentral/contrib/mdm/commands/managed_application_list.py)
  interprets reported states and then performs product-specific tracking and retry scheduling.
- **Possible approach.** Expose byte/manifest validation and report decoding independently.
  Derive checksum behaviour from the actual manifest format, and represent acknowledgement
  separately from a later installation report.
- **Unresolved questions.** Fleet's inspected helper sets `SHA256Size` to the digest length
  (32) while hashing the whole input; Apple's `sha256-size` describes a data chunk size.
  Investigate that discrepancy with inputs larger than 32 bytes rather than copying the
  constant. The inspected legacy helper does not establish DDM app/package coverage or
  general installation completion semantics.

**Addition.** Provide typed helpers for command-based installation/removal, managed app
configuration and applicable declarative app/package resources. Validate manifests and
metadata; verify caller-supplied artifact bytes using each format's actual integrity rules.
Do not impose one invented checksum convention on all Apple formats.

Interpret each installation result and application report without losing its original Apple
states or combining it into a general app lifecycle. Cover declarative app management as
well as legacy commands: Apple documents both
[deployment and conversion of existing managed apps](https://developer.apple.com/documentation/devicemanagement/deploying-apps-with-declarative-management?changes=latest_minor).
Expose documented conversion prerequisites as findings. The server selects and sequences
any conversion operations.

**Boundary.** Products own the app catalogue, current app state, desired assignments,
reconciliation, artifact hosting and operation sequence. Do not introduce a shared app
lifecycle model or app observation store. Any report helpers in F12 interpret the same
individual messages; they are not an installation-tracking service.

**Acceptance.** Fixtures preserve reported installing, managed, failed and unknown values.
Command acknowledgement is not converted into completed installation. Invalid metadata and
incorrect artifact bytes fail the applicable format checks. Check whole-file and chunked
hashes with inputs exceeding one chunk; do not confuse digest length with data chunk size.
Helpers use fixed caller identities and the pinned platform constraints without requiring
an app database or runner.

**Dependencies.** F5 and F8 for authoring. Share response interpretation with F12 where needed.
F9 is required only for examples that actually exercise Apple's licence service.

### F11. Software update primitives

**Problem to solve.** As a device management developer, I want to look up a specified update
in Apple's catalog, encode explicit update settings and decode update reports, so that I
can implement my chosen update process without reproducing Apple's formats and constraints.

**Baseline — bounded extension.** GDMF lookup, update-related generated types and an ADE
update gate already exist. F5 may cover much of the remaining construction work; a dedicated
software update package needs a demonstrated protocol gap beyond those existing APIs.

**Research context.**

- **Apple documentation.** [Declarative software updates](https://developer.apple.com/documentation/devicemanagement/deploying-software-updates-using-declarative-management)
  documents GDMF hardware identifiers, expiration and supplemental builds.
  [Enforcement phases](https://developer.apple.com/documentation/devicemanagement/phases-of-software-update-enforcement?changes=_8_1)
  distinguish catalog matching, organization-selected deadlines and device-reported progress.
  These give concrete lookup/encoding rules while leaving rollout choices to the product.
- **Other implementations.** [Fleet's GDMF code](https://github.com/fleetdm/fleet/blob/5e5976b269c8aa86c34d6ddd64024bee39437a23/server/mdm/apple/gdmf/api.go)
  combines catalog retrieval with version/device matching and latest-release helpers; its
  [tests](https://github.com/fleetdm/fleet/blob/5e5976b269c8aa86c34d6ddd64024bee39437a23/server/mdm/apple/gdmf/api_test.go) supply matching fixtures.
  [Zentral's catalog code](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/zentral/contrib/mdm/software_updates.py)
  stores release availability ranges. Its
  [enforcement builder](https://github.com/zentralopensource/zentral/blob/f47153eddb6d7882cce9878402c95b7d82795711/zentral/contrib/mdm/declarations/software_update.py)
  additionally selects releases from blueprint settings and derives deadlines from
  availability dates and policy delays.
- **Possible approach.** Extract test cases for explicit version/build matches, expired
  releases and missing device identifiers. Encode caller-provided deadlines through F5.
  Keep Zentral's release selection, calculated deadlines and application token format outside
  the library design.
- **Unresolved questions.** Compare each needed operation with existing GDMF/F5 APIs before
  introducing a workflow package. Hardware testing is still needed for local deadline
  behaviour and supplemental updates. A catalog match alone is not an installation guarantee.

**Addition.** Fill specific gaps in catalog queries for explicit versions, builds and listed
device support. Report what the catalog establishes, including missing information. Use F5
to construct each caller-selected declaration from explicit content and stable identities.
Decode individual Apple update states and reasons, preserving missing and unknown values.
Add standalone helpers only for Apple rules that catalog APIs and generated builders do not
already express; identify that rule before committing to a new package.

**Boundary.** The server selects the release, determines eligibility for its rollout, chooses
deadlines and deferrals, assembles declaration sets and selects status subscriptions. It
also combines reports into update progress and compliance. This entry does not add a builder
that resolves a release and assembles an update strategy, even with configurable inputs.

**Acceptance.** Catalog queries distinguish an explicit match, an expired release and absent
information. Fixed declaration inputs produce stable content and pass generated validation. Report decoding
retains Apple's states and reasons without issuing a compliance verdict. Examples show the
caller choosing each resource and operation. Hardware evidence qualifies any claims about
actual deadline behaviour.

**Dependencies.** Review existing GDMF and F5 first. F8 and F12 apply only to reference checks
and interpretation of individual reports; no new update state store is required.

### F12. Reusable protocol observations

**Problem to solve.** As a device management developer, I want to understand the contents,
scope and source of an individual device report, so that I can build my own inventory model
without misreading omitted fields or reproducing Apple's response rules.

**Baseline — narrowed extension; consolidated state deferred.** Command results are already
stored and DDM has status query/storage contracts. Some response families need interpretation
beyond their generated types. Those gaps do not establish a need for a new observation store.

**Research context.**

- **Apple documentation.** [InstalledApplicationList request rules](https://developer.apple.com/documentation/devicemanagement/installedapplicationlistcommand/command-data.dictionary?changes=_3)
  describe requested fields, managed-only filtering and omitted expensive values on newer
  systems. Managed-only/user-enrollment queries can exclude DDM-managed apps; omission is
  therefore not proof of removal. The pinned
  [command schema](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/mdm/commands/application.installed.list.yaml) records these cases.
  [StatusReport](https://developer.apple.com/documentation/devicemanagement/statusreport)
  explicitly distinguishes full replacement from incremental DDM reporting.
- **Other implementations.** [Fleet's app-result processing](https://github.com/fleetdm/fleet/blob/5e5976b269c8aa86c34d6ddd64024bee39437a23/server/service/apple_mdm_cmd_results.go)
  separates a parsed result carrying raw data and command/host identity from a handler that
  updates expected installations and issues further work.
  [KMFDDM's status parser](https://github.com/jessepeterson/kmfddm/blob/7f06151330d7827cc9b2b79332067f8d4fd3ceb4/ddm/status.go) keeps the raw report
  and dispatches paths to declaration, value and error handlers. These provide interpretation
  examples; their shapes are not a universal current-inventory contract.
- **Possible approach.** Keep response scope with parsed fields and preserve access to the
  original message. Test omitted/requested keys and full/partial DDM reports independently.
  Leave result-to-inventory updates and follow-up commands to consumers.
- **Unresolved questions.** No general cross-source observation model is established by these
  sources. Server receipt time cannot resolve device sampling order without additional
  evidence. Also distinguish command-level availability from per-key support: current Apple
  pages and the v26.4 schema are not identical in every support annotation.

**Addition.** Add helpers for individual device information, security, profile-list,
certificate-list and application reports only where Apple-defined interpretation is missing.
Reuse generated types and existing result records. Expose response scope, field presence,
source command, enrollment/channel and available timestamps or tokens. Distinguish server
receipt time from device observation time; receipt order alone cannot prove collection order.

Document whether a response is complete or scoped and what omissions mean where Apple
specifies that behaviour. Preserve existing protocol-defined DDM full/partial report
semantics. Reuse F2 extraction for enrollment fields. Do not merge separate command reports
or combine command and DDM evidence into a current device record.

**Boundary.** The server owns current inventory, source precedence, cross-report merge rules,
freshness, history and inferred changes. A generic observation store and automatic result
projections are deferred until a consumer demonstrates a separate, unavoidable protocol
requirement. An absent profile does not establish who removed it. Existing result and DDM
stores remain supported; this proposal does not replace their contracts.

**Acceptance.** A scoped report exposes its scope and does not claim a complete inventory.
Tests include managed-only app queries that exclude DDM-managed apps and supported requests
that omit expensive fields. Absent fields remain distinguishable from explicit values. Callers can identify the source
of each interpretation. Parsing a report does not modify other records, infer removal or
choose a winning observation. Tests cover existing DDM replacement/update semantics where
those paths change, without adding a second store for the same information.

**Dependencies.** Existing result types and DDM contracts; F4 for correlation where needed.
Coordinate helpers with F2, F10 and F11. No new storage or composition layer is a prerequisite.

## Integration and delivery

### F13. Embedding and storage portability

**Problem to solve.** As a device management developer, I want independently usable component
APIs, assembly examples and supported store migrations, so that I can choose my server's
architecture while relying on the library to maintain the protocol state it stores.

**Baseline — extension; general composition deferred.** Public services, adapters and SQL
stores already exist. Reference composition lives in `server/internal/app`; migration sets
are separate. Enrollment export has
[explicit inclusions and exclusions](../research/decisions/0017-enrollment-export-import.md).

**Research context.**

- **Apple documentation.** [Check-in](https://developer.apple.com/documentation/devicemanagement/check-in)
  describes the enrollment identity and token exchanges that implementations must preserve.
  Apple does not prescribe a Go store interface, database migration coordinator or application
  assembly API. Requirements for those mechanisms must come from this library's owned state
  and backend guarantees.
- **Other implementations.** [NanoMDM's migration interface](https://github.com/micromdm/nanomdm/blob/d61174ca4e386674067cc99a6b93215f86dc88f4/storage/migrate.go)
  exports check-in messages. Its [file implementation](https://github.com/micromdm/nanomdm/blob/d61174ca4e386674067cc99a6b93215f86dc88f4/storage/file/migrate.go)
  reconstructs exports from stored check-ins and explicitly notes that separately stored unlock
  tokens need synthesis into exported messages. This exposes a limitation of replay-only
  migration. [Fleet's storage adapter](https://github.com/fleetdm/fleet/blob/5e5976b269c8aa86c34d6ddd64024bee39437a23/server/datastore/mysql/nanomdm_storage.go)
  embeds NanoMDM's MySQL store and adds application dependencies/overrides.
  [ddm_infra](https://github.com/macadmins/ddm_infra/blob/7ae13840f84be72baaaceb29205d84048d63dbeb/README.md) demonstrates deployment wiring, which
  belongs in examples or a product rather than a mandatory composition API.
- **Possible approach.** Keep this library's explicit export records and document every
  included/excluded state category. Test parent-first import and separately escrowed values.
  Make component dependencies public without adopting Fleet's application wrapper.
- **Unresolved questions.** No matching selected-store migration coordinator was found in
  the inspected NanoMDM migration contract, file exporter or Fleet adapter. That API still
  needs a concrete dependency/error contract across this library's stores. None of these
  examples demonstrates a need for general server composition.

**Addition.** Demonstrate direct assembly through external-package examples and fix specific
component APIs that prevent it. Keep required dependencies explicit. Provide migration
metadata and a coordinator for caller-selected library store sets, ordered by dependency,
with applied versions and failures reported. Do not imply atomic migration across sets or
database dialects.

Maintain versioned export/import for the protocol state the library actually owns. Extend
it only when an accepted feature adds relevant persisted state; document excluded sessions,
queues and other operations. New store contracts must justify their protocol responsibility
and fit independently usable components. Apply existing secret-handling contracts to any
new credential material.

**Boundary.** The server owns application structure, service composition, configuration,
listeners, loops and operational migration sequencing. General public composition helpers
are deferred until real consumers demonstrate identical wiring worth extracting. Examples
must not become a mandatory framework. A selected-store migration utility does not choose
the application's stores or initialize every optional subsystem.

**Acceptance.** External-package examples assemble components through public APIs without
depending on a general composition package. Selected migrations are repeatable, respect
dependencies and report failures. Existing supported export/import paths and backend
contracts continue to work. Root-module and schema-layer dependency checks pass. Any new
public assembly helper requires evidence of repeated identical wiring before inclusion.

**Dependencies.** Deliver component and migration improvements with concrete consumers.
General composition is not a prerequisite or a scheduled deliverable.

### Sequence

| Stage | Work | Exit condition |
|---|---|---|
| Establish evidence | F1; start F3 hardware investigation and collect F7 reference examples | Baseline coverage and unresolved protocol questions are explicit |
| Improve foundations | F2 extraction/assessment, F4 exchange contracts and F5 authoring | Explicit facts produce findings; exchange correctness is independent of queue policy; authoring preserves identities |
| Improve interpretation | F6, F7 and F8; specific F12 report helpers | Inspection and diagnostics operate on supplied evidence without maintaining current inventory |
| Broaden protocol primitives | F9 and F10; F11 only for demonstrated gaps beyond GDMF and F5 | Apple operations and formats are usable without a workflow, lifecycle store or update strategy |
| Continue integration | F13 component examples and selected-store migrations | Components remain independently usable; a composition framework is unnecessary |

Stages are priorities, not a waterfall. Client development, reference example collection and
hardware testing can proceed while foundation APIs are refined. Do not attach calendar dates
or minor-version promises until the evidence, compatibility impact and implementation size
are understood.

Consolidated enrollment facts, general observation storage and general server composition
are outside these stages. Reconsider them only when actual consumers identify protocol
requirements that the narrower APIs and existing stores cannot meet.

### Completion gates

- **Ownership:** identify the Apple rule or unavoidable protocol bookkeeping each addition
  implements. Distinguish that from a product choice. Generic usefulness, purity or an
  interface alone is insufficient; new packages must also justify what existing APIs lack.
- **Protocol evidence:** link Apple claims to the pinned schema or specific documentation.
  Distinguish documented, simulated, hardware-observed and unresolved behaviour. Record OS
  version/build, enrollment method and sanitized inputs/results for hardware investigations.
- **Meaningful verification:** use focused unit tests, tests against reference examples,
  store contract suites, relevant concurrency/failure tests and simulator scenarios. Run
  generation/export checks when generated APIs change and layout checks when dependencies change.
- **Storage semantics:** for accepted protocol state, define atomicity, duplicate handling,
  lifecycle and export compatibility. Keep report scope separate from cross-report merge
  policy. Retain evidence when the protocol cannot establish ordering or a current value.
- **Consumer proof:** demonstrate runtime capabilities through small external-package examples
  with explicit caller choices. Maintenance work such as F1 needs repository checks and
  documentation instead. Neither category requires a running reference server.
- **Compatibility:** identify changed signatures, zero-value behaviour, optional contracts
  and migration requirements. Do not silently change scheduling, admission or support policy.

Hardware resources should match the behaviour under test: ADE and profile-enrolled Macs,
supervised and unsupervised iPhones, eligible user channels, an Apps and Books token and a
device eligible for an actual software update. Hardware-dependent claims remain provisional
until recorded. A passing simulator demonstrates the model's consistency, not Apple-client
interoperability.

### Reserved for the future opinionated server

The future server owns the consolidated device model: authoritative enrollment facts,
current inventory, source precedence, freshness and inferred changes. It also owns app
lifecycle tracking, update strategies, declaration-set assembly, subscription selection,
queue scheduling, fleet diagnosis and repair, and application composition.

Generic desired-state plan/apply, fleet targeting languages, compliance scoring,
reconciliation, workflow engines, artifact hosting, tenancy, administrative policy and
UI-shaped APIs remain outside this roadmap. Library helpers must not silently rotate profile
identities, repair declaration membership or change product policy on a release schedule.

Products can consume extracted facts, findings, catalog results, explicit commands and
declarations, operation results and existing stored protocol reports. The library supplies
the evidence and operations needed to build a server. The product decides how that evidence
describes its devices and what should happen next.
