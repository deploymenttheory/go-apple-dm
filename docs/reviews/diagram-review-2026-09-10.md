# Diagram review after PRs 12 and 14

Reviewed on 10 September 2026 against [`ff85958196cb`](https://github.com/deploymenttheory/go-apple-dm/commit/ff85958196cb013e8e14c1055f067319b07e3299). All **27 existing diagrams** have been assessed; this package also specifies **four companion diagrams**. The work changes review documents only. No diagram JSON, HTML, or README image has been regenerated.

The implementation comparison includes merged [PR 12](https://github.com/deploymenttheory/go-apple-dm/pull/12) (merge 9c3f4b1717ff938cfa2a44ec7c2601b347d549c0) and [PR 14](https://github.com/deploymenttheory/go-apple-dm/pull/14) (merge 91fd45e). Current code takes precedence over historical PR descriptions.

The current drawings are a useful implementation map, but several are misleading as an introduction. Four need immediate semantic correction: check-in dispatch, the ADE sequence, command retry state, and enrollment lifecycle. The rest need missing behaviour, clearer responsibilities, or terminology updates.

## How to use this review

- This report gives the finding, evidence, structural correction, and public references for every diagram.
- [Replacement copy](diagram-review-2026-09-10.copy.md) gives exact field-by-field wording, keyed by the existing element IDs and JSON pointers.
- [Machine-readable specification](diagram-review-2026-09-10.copy.json) contains old/new values, explicit retained fields, evidence hashes, source hashes, public reference checks, and browser measurements. It is a review format, **not Archify input or an executable JSON Patch**.
- [Companion specifications](diagram-review-2026-09-10.companions.md) provide complete node, relationship, card, and branch wording for the four additions.

Apply topology decisions before copy changes. An instruction to remove an element takes precedence over a copy entry for that old element. New geometry is deliberately not prescribed: Archify validation and browser inspection must establish that the longer copy fits. Existing IDs remain stable where their meaning remains the same.

## Findings that affect more than one diagram

1. **Replacement is not ordinary re-enrollment.** Ordinary Authenticate upserts can clear working state. PR 14 stages a replacement identity and waits for candidate authentication, a device TokenUpdate, and acknowledgement of the delivered InstallProfile. The final qualifying event commits the candidate atomically; acknowledgement need not arrive last. Failed, cancelled, and expired attempts retain the server’s working state. Apple documents profile replacement, while the exact staging contract is this repository’s design. See [Apple’s enrollment-profile guidance](https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles) and [the shared transition implementation](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/storage/replacement.go#L72).
2. **MDM push, app push, and identity issuance have different credentials.** PR 12 added app provider credentials and send APIs. Keep the MDM customer push certificate, MDM vendor CSR-signing identity, app provider certificate, and device identity certificate distinct. APNs acceptance does not establish device receipt or command completion. See [MDM push setup](https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers), [app provider certificates](https://developer.apple.com/documentation/usernotifications/establishing-a-certificate-based-connection-to-apns), and [app send handling](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/apppush.go#L141).
3. **Arrows must describe what actually happens.** A dispatch map must not invent a required chronology. An optional web-auth branch must not appear mandatory. A store is not the actor that verifies an attestation binding, and a software catalogue does not send the enrollment endpoint’s HTTP rejection.
4. **Native TLS trust and device identity are separate.** PR 14 distributes only explicitly configured HTTPS anchors, adds /MDMServiceConfig, and persists enrollment-profile identity metadata. Explain these beside profile delivery, not by renaming the issuing CA “trust”. See [service discovery and trust](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/serviceconfig.go#L29) and [the MDM payload](https://developer.apple.com/documentation/devicemanagement/mdm).
5. **The shared runtime and bench are now first-class parts of the repository.** Include native TLS, readiness, HTTP-before-worker shutdown, process acceptance, and live evidence. A coverage threshold or simulated scenario is not a claim of a passing gate or hardware verification. PR 14’s [validation record](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/docs/testing/enrollment-validation.md) reports per-package coverage failures and unverified real-device flows.

## Language and reading contract

Use Apple’s “Automated Device Enrollment”, “account-driven User Enrollment”, “account-driven Device Enrollment”, “Managed Apple Account”, and “declarative device management”. Preserve protocol strings such as Authenticate, TokenUpdate, PushMagic, ServerURL, CheckInURL, and InstallProfile. Introduce dep as the repository’s device-assignment client, not as the modern name of an enrollment method.

Name the actor or responsibility in the main label; use the second line to explain its action or output. Keep code identifiers in source references or explanatory text when they help locate implementation. Distinguish a macOS bootstrap token from the static root administration token. Describe a certificate pin as the stored identity fingerprint on first use. Use “rejected” for an unsuccessful request, “disabled” for an enrollment, and “failed” for a recorded failed attempt.

The main diagram must communicate its trigger, actors, decisions, and outcome without guided views. Guided views add explanation rather than repairing a misleading main picture. Timing defaults, error codes, and internal method names belong in supporting text unless essential to the main relationship.

## Rendered-artifact observations

Fresh Chrome captures of all 27 current HTML artifacts were inspected in the light theme at 1440×900, with hashes bound to the reviewed files. All fit within the document’s measured scroll bounds. Nevertheless, supporting node text projects to roughly 6–8 pixels in several diagrams; request-decode measured approximately 6.03 pixels and lifecycle-command 6.17 pixels. The check-in dispatch image visibly reinforces the false mandatory sequence. Some wide viewer toolbars also run to the right edge in the captures. A scroll-bound check alone is insufficient evidence of readability or complete viewer-control visibility.

Revise layout to support explanatory copy at a comfortable initial scale: aim for at least 14 CSS pixels for primary labels and 12 for essential secondary text at 1440×900. This is an editorial target, not an existing Archify acceptance threshold. Use wider nodes, simpler supporting topology, and the four focused companion diagrams before shrinking text. Recheck toolbar bounds explicitly. Do not hide overflow or require zooming to understand the main flow.

These are supplementary observations, not a new 9/9 delivery receipt or a full theme/viewport acceptance pass. The existing receipt in v0.1.0-diagrams.json pins the earlier revision a817270f13b4ef7a9b1d2895118981120546b3a2; architecture JSON source pins are also old. A later implementation must regenerate those pins, HTML artifacts, and both README architecture images.

## References inside the diagrams

Every proposal includes a visible **Public documentation** card with the exact document title and URL. The report and copy document provide clickable links. References are selected per subject: Apple for device protocols, RFCs for standards, and Go/Cedar documentation for implementation concepts. Repository rules such as tier exceptions, queue backoff, replacement staging, and coverage thresholds cite code rather than attributing those choices to Apple.

The installed Archify 2.17 schema supports plain string card items; architecture component sources accept repository file paths, not arbitrary public URLs. It has no dedicated external-reference link field. Therefore the conservative implementation is a plain string title-and-URL card, with clickable references also retained in the diagram catalogue. Do not insert HTML/Markdown into card strings or invent schema fields and assume they become links. Actual clickable references inside the viewer require explicit schema/renderer support and a separate decision; none is claimed here. Long reference cards must be laid out and visually checked with the diagram, not clipped or shrunk to meet containment.

All 31 selected public reference documents were retrieved successfully. Apple’s Markdown representations were used where available so the content, not just a JavaScript landing page, could be checked. One guessed attestation URL returned 404 and was replaced with the verified Platform Deployment page. /MDMServiceConfig’s exact emitted fields are supported here by implementation and tests; the broader Apple profile references should not be read as documentation of those exact route fields.

## Per-diagram assessment

P0: correct before onboarding use. P1: material coverage or explanation changes. P2: terminology and explanatory refinement. Each diagram retains its useful purpose; no blanket redesign is proposed.

| Diagram | Priority | Main correction |
|---|---|---|
| [system-architecture](#system-architecture) | P1 | The overview predates both PRs. It omits service discovery, shared runtime, app push, and controlled profile replacement; “DDM has no device URL” needs to be qualified as this server’s transport choice. |
| [package-layering](#package-layering) | P1 | “Never to its left” overstates the enforced import rule. The tier test explicitly permits enroll/ade → gdmf and excludes test scaffolding. It also classifies server-prefixed composition packages as server, despite the conceptual app-tier label. |
| [storage-contract](#storage-contract) | P1 | Eight composed interfaces is still correct. ReplacementStore is an optional extension. The picture overstates encryption as an interface guarantee and ties bootstrap-token escrow to FileVault. |
| [storage-backends](#storage-backends) | P1 | The diagram omits encrypted replacement state and the apppush/v1/ state namespace. Encryption is conditional for core SQL stores; app push persistence requires a keyring. A blanket rewrap claim would be wrong. |
| [service-layer](#service-layer) | P1 | The service view omits replacement interception and asynchronous audit cancellation behaviour. “Seams and refusal” and numeric-only error summaries do not explain responsibilities. |
| [checkin-dispatch](#checkin-dispatch) | P0 | A dispatch diagram incorrectly connects Authenticate → TokenUpdate → SetBootstrapToken → CheckOut as a required chain. Authorization appears as a sibling branch; PR 14 adds an earlier replacement path. Bootstrap escrow is not conditional solely on FileVault. |
| [ddm-engine](#ddm-engine) | P1 | The architecture remains useful, but cleanup on Authenticate now needs the controlled-replacement exception. The engine validates supported activation-predicate syntax; device activation evaluation must not be presented as a server-side decision. Change records and snapshots need their purposes explained. |
| [ddm-serve](#ddm-serve) | P1 | ParseEndpoint does not inspect status data; Handle and Status do that later. The diagram leaves declaration and status success paths dangling, and “hash it” conflates manifest token computation with response serialization. |
| [acme-internals](#acme-internals) | P1 | The JWS helper is drawn as consuming nonces, and policy as directly invoking the signer. In code acme.Server orchestrates these operations; certificate signing follows CSR finalization and key matching. |
| [push](#push) | P1 | This is specifically the MDM wake path. PR 12 adds app push and connection retirement; the current heading and certificate summary do not make the separation clear. |
| [apple-service-clients](#apple-service-clients) | P1 | DEP should be introduced as the repository name for the device-assignment API. The axm arrow elides the OAuth token exchange. Syncer calls both FetchDevices and SyncDevices; readback occurs against Apple, not merely the local store. |
| [admin-plane](#admin-plane) | P1 | The route families predate app push, enrollment-profile issuance, replacement, evidence, and command-result APIs. The universal principal→Cedar wording contradicts the documented static-root bypass; “0600 or refused” is stricter than the actual group/other-bit check. |
| [split-deployment](#split-deployment) | P1 | The signed adapter boundary remains valid. “Role all” is too narrow for the in-process fallback, and the hop should not imply native mTLS configuration that only the library exposes. Runtime and bench coverage are missing. |
| [schema-generation](#schema-generation) | P2 | The flow remains relevant, but the existing description calls all support code handwritten despite emit_support.go generating support tables. Clarify generated data versus handwritten policy and show verification rather than implying source mutation during normal use. |
| [request-decode](#request-decode) | P1 | The drawing shows only the signature identity source while the card names three. “Pinned” is attributed to decoding even though service authorization performs association and checks, including PR 14’s candidate path. |
| [reference-server](#reference-server) | P1 | The build-only view omits PR 12’s shared runtime, native TLS, readiness, and shutdown. App.wire registers services; it does not start workers. “One *sql.DB” is false for memory mode. |
| [enrollment-paths](#enrollment-paths) | P1 | The title implies complete enrollment coverage but shows only ADE and account-driven paths. PRs 12/14 add maintained OTA/manual administration, service discovery, stable profiles, and independent HTTPS trust. SCEP/ACME are identity methods, not enrollment types. |
| [test-harness](#test-harness) | P1 | The diagram invents a unit→fuzz→contract pipeline and omits shared embedded/process scenarios and live evidence. The 95% gate is a threshold, not evidence that the current revision passes it. PR 14 records a failing per-package coverage gate. |
| [flow-dep-sync-assign](#flow-dep-sync-assign) | P2 | The core flow is sound. Replace legacy DEP-first prose, explain the public-certificate/token exchange, and avoid presenting enrollment-profile assignment as device enrollment itself. |
| [flow-ade-enrollment](#flow-ade-enrollment) | P0 | The sequence shows web authentication unconditionally after a POST, although only GET with WebAuth configured takes that branch. It also makes the software catalogue return the device-facing HTTP 403 and depicts an OIDC callback as a direct provider→server call. |
| [flow-account-driven-enrollment](#flow-account-driven-enrollment) | P1 | The response arrow “reserve, persist, confirm” exposes internal steps as if they were a wire response. Discovery names only mdm-byod, certificate issuance is collapsed into profile delivery, and the sequence stops before TokenUpdate. |
| [flow-command-delivery](#flow-command-delivery) | P1 | The happy path hides Idle, no-command responses, and completion persistence. PR 14’s replacement InstallProfile is delivered through a separate staged path rather than the ordinary queue shown here. |
| [flow-ddm-sync](#flow-ddm-sync) | P1 | The diagram visually connects devices directly to the engine, omits the wake/poll exchange, and leaves response content implicit. Engine arrows should be logical calls with the transport stated, not public HTTP endpoints. |
| [flow-acme-attestation](#flow-acme-attestation) | P1 | The sequence omits obtaining a replay nonce and shows certificate download as an unsolicited response after finalization. Binding comparison belongs to the coordinator, not a store RPC. Explain CSR key matching at finalization. |
| [flow-scep-issuance](#flow-scep-issuance) | P1 | The challenge response “derived from the common name” incorrectly generalizes the HMAC strategy to static and one-use credentials. Success and failure are drawn as successive replies; PR 14 adds CSR-bound replacement authorization. |
| [lifecycle-command](#lifecycle-command) | P0 | The NotNow card implies that only backoff-pending commands are skipped on the current connection, but skipNotNow skips every NotNow command. Error/CommandFormatError, redelivery of sent commands, and clearing from pending/not-now are missing. |
| [lifecycle-enrollment](#lifecycle-enrollment) | P0 | The lifecycle conflates accepted ordinary re-enrollment with identity rotation and treats rejected reuse as a persisted enrollment state. It omits controlled replacement and implies checkout permanently prevents enrollment. |

### system-architecture

**P1 — The overview predates both PRs. It omits service discovery, shared runtime, app push, and controlled profile replacement; “DDM has no device URL” needs to be qualified as this server’s transport choice.**

Evidence: [server/internal/app/app.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/app.go#L494), [server/internal/app/enroll.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/enroll.go#L508), [server/internal/app/serviceconfig.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/serviceconfig.go#L29), [server/internal/app/apppush.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/apppush.go#L141), [server/internal/runtime/runtime.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/runtime/runtime.go#L35).

Proposed structure:

- Keep the overview at component level. Add app-push as a separate component connected from adminapi to apns; label it “App notifications” / “Explicit app topic and environment”. Do not route it through pushnotify.Notifier.
- Add adminapi → core: “Queue commands and prepare profile replacement”. Add a runtime boundary caption “Shared HTTP and worker lifecycle”. Put discovery detail in the enrollment diagrams and runtime detail in reference-server.

Public context: [Device Management](https://developer.apple.com/documentation/devicemanagement), [Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device), [Integrating declarative management](https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management), [MDM payload](https://developer.apple.com/documentation/devicemanagement/mdm).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#system-architecture). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### package-layering

**P1 — “Never to its left” overstates the enforced import rule. The tier test explicitly permits enroll/ade → gdmf and excludes test scaffolding. It also classifies server-prefixed composition packages as server, despite the conceptual app-tier label.**

Evidence: [internal/layout/layout_test.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/internal/layout/layout_test.go#L98), [internal/layout/layout.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/internal/layout/layout.go#L1), [go.mod](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/go.mod#L1), [server/go.mod](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/go.mod#L1).

Proposed structure:

- Keep the nine conceptual tiers, but label the diagram as a conceptual package map. Do not claim the test classifies server/internal/app, runtime, bench, or dmctl into its app tier.
- Add a dashed exception edge protocoltier → clienttier labelled “ADE software-update lookup”; annotate it as the exact enroll/ade → gdmf exception.
- Retain existing unlabeled arrows: the global key “imports” supplies their meaning; they make no runtime-call claim.

Public context: [Go Modules Reference](https://go.dev/ref/mod), [Apple device-management schema repository](https://github.com/apple/device-management).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#package-layering). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### storage-contract

**P1 — Eight composed interfaces is still correct. ReplacementStore is an optional extension. The picture overstates encryption as an interface guarantee and ties bootstrap-token escrow to FileVault.**

Evidence: [storage/storage.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/storage/storage.go#L335), [storage/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/storage/replacement.go#L72), [server/sqlstore/sqlcommon/seal.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/sqlstore/sqlcommon/seal.go#L1), [storage/storagetest/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/storage/storagetest/replacement.go#L17).

Proposed structure:

- Keep all eight interfaces inside the storage.Store group. Add a separate “Optional replacement contract” card rather than a ninth composed interface.
- Change the push certificate write edge from an encryption claim to a validation-and-storage action.

Public context: [Check-in](https://developer.apple.com/documentation/devicemanagement/check-in), [Set Bootstrap Token](https://developer.apple.com/documentation/devicemanagement/set-bootstrap-token), [Go cipher.NewGCM](https://pkg.go.dev/crypto/cipher#NewGCM).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#storage-contract). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### storage-backends

**P1 — The diagram omits encrypted replacement state and the apppush/v1/ state namespace. Encryption is conditional for core SQL stores; app push persistence requires a keyring. A blanket rewrap claim would be wrong.**

Evidence: [server/sqlstore/sqlcommon/seal.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/sqlstore/sqlcommon/seal.go#L1), [server/sqlstore/sqlcommon/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/sqlstore/sqlcommon/replacement.go#L1), [server/apppush/store.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/apppush/store.go#L68), [server/internal/app/app.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/app.go#L494), [storage/inmem/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/storage/inmem/replacement.go#L1).

Proposed structure:

- Add state.Store to feature storage; show apppush/v1/ as a namespace in it, not a new SQL table or a PushCertStore implementation.
- Keep the SQL dialect and shared connection-pool relationships. Note that memory mode has no SQL pool.

Public context: [Go cipher.NewGCM](https://pkg.go.dev/crypto/cipher#NewGCM), [Go Modules Reference](https://go.dev/ref/mod).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#storage-backends). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### service-layer

**P1 — The service view omits replacement interception and asynchronous audit cancellation behaviour. “Seams and refusal” and numeric-only error summaries do not explain responsibilities.**

Evidence: [server/service/checkin.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/checkin.go#L29), [server/service/connect.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/connect.go#L39), [server/service/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/replacement.go#L40), [server/httpapi/httpapi.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/httpapi/httpapi.go#L1), [mdmprotocol/event/event.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/event/event.go#L1), [server/internal/app/adminaudit.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/adminaudit.go#L50).

Proposed structure:

- Keep the component diagram. Add core → replacement contract with label “Stage or commit profile replacement”. Show this as the optional replacement path, not a universal certificate bypass.
- Explain status checking before hooks and replacement handling before ordinary dispatch in cards; do not fabricate a single linear authorization path for all message types.

Public context: [Check-in](https://developer.apple.com/documentation/devicemanagement/check-in), [Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device), [Declarative Management check-in](https://developer.apple.com/documentation/devicemanagement/declarative-management).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#service-layer). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### checkin-dispatch

**P0 — A dispatch diagram incorrectly connects Authenticate → TokenUpdate → SetBootstrapToken → CheckOut as a required chain. Authorization appears as a sibling branch; PR 14 adds an earlier replacement path. Bootstrap escrow is not conditional solely on FileVault.**

Evidence: [server/service/checkin.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/checkin.go#L29), [server/service/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/replacement.go#L40), [server/service/userauth.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/userauth.go#L1), [server/httpapi/httpapi.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/httpapi/httpapi.go#L1).

Proposed structure:

- Replace the workflow topology with a dispatch architecture map, keeping the basename. Delete c2, c3, c4 and the misleading sequential mainPath.
- Use one entry component “Check-in service” / “Status checks, hooks, and replacement handling”, then dispatch to all nine message handlers independently. Preserve protocol message identifiers as labels.
- Represent ordinary handler authorization as a shared policy annotation, not an every-message call after dispatch. Keep HTTP errors in a card instead of drawing every branch through a single refusal node.

Public context: [Check-in](https://developer.apple.com/documentation/devicemanagement/check-in), [Set Bootstrap Token](https://developer.apple.com/documentation/devicemanagement/set-bootstrap-token), [Declarative Management check-in](https://developer.apple.com/documentation/devicemanagement/declarative-management).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#checkin-dispatch). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### ddm-engine

**P1 — The architecture remains useful, but cleanup on Authenticate now needs the controlled-replacement exception. The engine validates supported activation-predicate syntax; device activation evaluation must not be presented as a server-side decision. Change records and snapshots need their purposes explained.**

Evidence: [mdmprotocol/ddm/engine.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/ddm/engine.go#L1), [mdmprotocol/ddm/serve.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/ddm/serve.go#L156), [mdmprotocol/ddm/predicate_check.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/ddm/predicate_check.go#L1), [server/ddmsync/notifier.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/ddmsync/notifier.go#L1), [server/ddmsync/servicehook.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/ddmsync/servicehook.go#L1), [server/service/checkin.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/checkin.go#L29).

Proposed structure:

- Keep the engine/notifier/store topology. Qualify e3 as ordinary Authenticate/CheckOut cleanup; replacement calls are renamed and do not trigger that cleanup.

Public context: [Integrating declarative management](https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management), [Declarations](https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations), [RFC 8785 — JSON Canonicalization Scheme](https://www.rfc-editor.org/rfc/rfc8785).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#ddm-engine). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### ddm-serve

**P1 — ParseEndpoint does not inspect status data; Handle and Status do that later. The diagram leaves declaration and status success paths dangling, and “hash it” conflates manifest token computation with response serialization.**

Evidence: [mdmprotocol/ddm/serve.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/ddm/serve.go#L156), [mdmprotocol/ddm/endpoint.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/ddm/endpoint.go#L52), [mdmprotocol/ddm/status.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/ddm/status.go#L1), [mdmprotocol/ddm/serve_test.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/ddm/serve_test.go#L1).

Proposed structure:

- Add successful edges declitems → response (“Return declaration manifest”), declaration → response (“Return the advertised declaration version”), status → response (“Acknowledge stored status”).
- Keep syntax rejection at parse; move missing status data to the status error branch. Replace the tokens→canonjson label with canonical response serialization.

Public context: [Declarative Management check-in](https://developer.apple.com/documentation/devicemanagement/declarative-management), [Declarations](https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations), [RFC 8785 — JSON Canonicalization Scheme](https://www.rfc-editor.org/rfc/rfc8785).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#ddm-serve). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### acme-internals

**P1 — The JWS helper is drawn as consuming nonces, and policy as directly invoking the signer. In code acme.Server orchestrates these operations; certificate signing follows CSR finalization and key matching.**

Evidence: [pki/acme/server.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/pki/acme/server.go#L249), [pki/acme/handlers.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/pki/acme/handlers.go#L579), [pki/acme/policy.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/pki/acme/policy.go#L1), [pki/acme/attest/attest.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/pki/acme/attest/attest.go#L1).

Proposed structure:

- Change e4 from jose → nonces to server → nonces. Change e11 from policy → capkg to server → capkg and label it “Finalize: verify CSR, then issue”.
- Retain policy as a dependency that returns an admission decision, not a certificate issuer. Group supporting stores so the main path remains readable.

Public context: [ACMECertificate](https://developer.apple.com/documentation/devicemanagement/acmecertificate), [Deploy Managed Device Attestation](https://support.apple.com/en-gb/guide/deployment/dep54e5ac1fd/web), [RFC 8555 — ACME](https://www.rfc-editor.org/rfc/rfc8555).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#acme-internals). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### push

**P1 — This is specifically the MDM wake path. PR 12 adds app push and connection retirement; the current heading and certificate summary do not make the separation clear.**

Evidence: [server/pushnotify/notifier.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/pushnotify/notifier.go#L1), [server/pushnotify/certstore.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/pushnotify/certstore.go#L1), [appleplatformservices/push/apns/apns.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/appleplatformservices/push/apns/apns.go#L1), [appleplatformservices/push/apns/lifecycle.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/appleplatformservices/push/apns/lifecycle.go#L1), [server/internal/app/push.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/push.go#L1).

Proposed structure:

- Keep app notification delivery in its companion diagram. Add a device endpoint after APNs so the reader sees the purpose of the notification; label APNs → device “Prompt a connection to the MDM server”.
- Keep invalid-token events as outcomes, not automatic token deletion.

Public context: [Setting up push notifications for your device management customers](https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers), [Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device), [Establishing a certificate-based connection to APNs](https://developer.apple.com/documentation/usernotifications/establishing-a-certificate-based-connection-to-apns).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#push). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### apple-service-clients

**P1 — DEP should be introduced as the repository name for the device-assignment API. The axm arrow elides the OAuth token exchange. Syncer calls both FetchDevices and SyncDevices; readback occurs against Apple, not merely the local store.**

Evidence: [appleplatformservices/dep/syncer.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/appleplatformservices/dep/syncer.go#L1), [appleplatformservices/dep/assigner.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/appleplatformservices/dep/assigner.go#L1), [server/internal/app/dep.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/dep.go#L1), [appleplatformservices/axm/auth.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/appleplatformservices/axm/auth.go#L193), [appleplatformservices/axm/client.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/appleplatformservices/axm/client.go#L1), [appleplatformservices/gdmf/gdmf.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/appleplatformservices/gdmf/gdmf.go#L1).

Proposed structure:

- Keep the three service families. Represent axm authentication as client assertion → token exchange → bearer API calls in a card, avoiding a false assertion-direct-to-resource arrow.
- Rename assigner → depstore to local assignment-state access; include readback in assigner → depclient.

Public context: [Device assignment](https://developer.apple.com/documentation/devicemanagement/device-assignment), [Implementing OAuth for the Apple School Manager and Apple Business APIs](https://developer.apple.com/documentation/apple-school-and-business-manager-api/implementing-oauth-for-the-apple-school-manager-and-apple-business-api), [ErrorCodeSoftwareUpdateRequired](https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#apple-service-clients). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### admin-plane

**P1 — The route families predate app push, enrollment-profile issuance, replacement, evidence, and command-result APIs. The universal principal→Cedar wording contradicts the documented static-root bypass; “0600 or refused” is stricter than the actual group/other-bit check.**

Evidence: [server/internal/app/adminauthz.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/adminauthz.go#L1), [server/internal/app/adminbench.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/adminbench.go#L1), [server/internal/app/apppush.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/apppush.go#L141), [server/internal/dmctl/config.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/dmctl/config.go#L1), [server/adminauth/adminauth.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/adminauth/adminauth.go#L1).

Proposed structure:

- Keep the static root route and stored-principal authorization route visibly distinct. Both lead to a permitted handler.
- Add route coverage in a card instead of adding a node per endpoint. Keep offline dmctl explain and bench supervision outside the universal admin HTTP path.

Public context: [Cedar authorization overview](https://docs.cedarpolicy.com/), [Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device), [Deploying device management enrollment profiles](https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#admin-plane). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### split-deployment

**P1 — The signed adapter boundary remains valid. “Role all” is too narrow for the in-process fallback, and the hop should not imply native mTLS configuration that only the library exposes. Runtime and bench coverage are missing.**

Evidence: [server/ddmadapter/proxyclient/proxyclient.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/ddmadapter/proxyclient/proxyclient.go#L1), [server/ddmadapter/proxyserver/proxyserver.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/ddmadapter/proxyserver/proxyserver.go#L1), [server/ddmadapter/internal/proxywire/proxywire.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/ddmadapter/internal/proxywire/proxywire.go#L1), [server/internal/app/app.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/app.go#L494), [server/internal/runtime/runtime.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/runtime/runtime.go#L35).

Proposed structure:

- Add engine-store label “Persist declarative state”. Keep this as a dependency illustration, not an assertion that the two processes have separate databases.
- Annotate the in-process branch as the fallback when DM_DDM_URL is unset. Keep the two HMAC keys explicit; put optional custom-client mTLS in a library-capability note.

Public context: [Declarative Management check-in](https://developer.apple.com/documentation/devicemanagement/declarative-management), [RFC 9440 — Client-Cert HTTP Header Field](https://www.rfc-editor.org/rfc/rfc9440).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#split-deployment). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### schema-generation

**P2 — The flow remains relevant, but the existing description calls all support code handwritten despite emit_support.go generating support tables. Clarify generated data versus handwritten policy and show verification rather than implying source mutation during normal use.**

Evidence: [internal/schemagen/generate.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/internal/schemagen/generate.go#L1), [internal/schemagen/emit_support.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/internal/schemagen/emit_support.go#L1), [internal/schemagen/generatedfrom.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/internal/schemagen/generatedfrom.go#L1), [internal/schemagen/naming.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/internal/schemagen/naming.go#L1), [Makefile](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/Makefile#L1).

Proposed structure:

- Retain the data-flow structure. Treat the names lock and provenance as verification artifacts; make verify detects generated-output differences.

Public context: [Apple device-management schema repository](https://github.com/apple/device-management), [Go Modules Reference](https://go.dev/ref/mod).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#schema-generation). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### request-decode

**P1 — The drawing shows only the signature identity source while the card names three. “Pinned” is attributed to decoding even though service authorization performs association and checks, including PR 14’s candidate path.**

Evidence: [server/httpapi/cert.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/httpapi/cert.go#L1), [server/httpapi/httpapi.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/httpapi/httpapi.go#L1), [mdmprotocol/mdm/checkin.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/mdm/checkin.go#L1), [mdmprotocol/plist/plist.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/plist/plist.go#L1), [server/service/checkin.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/checkin.go#L29), [server/service/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/replacement.go#L40).

Proposed structure:

- Add tls → cert (“Verified TLS identity”) and proxy → cert (“Certificate from trusted proxy”) as explicit alternative identity inputs. Label new nodes “TLS client certificate” / “Verified by the TLS listener” and “Trusted proxy header” / “Accepted only at the proxy boundary”.
- Add body → cms (“Verify signature over these bytes”); signature verification depends on both the header and body.
- Change cms→cert classification from “pinned” to “verified signer”. Add a downstream service-authorization node “Authorize enrollment identity” / “Pin policy and replacement checks”, reached by typed message and certificate outputs.

Public context: [Check-in](https://developer.apple.com/documentation/devicemanagement/check-in), [RFC 9440 — Client-Cert HTTP Header Field](https://www.rfc-editor.org/rfc/rfc9440), [MDM payload](https://developer.apple.com/documentation/devicemanagement/mdm).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#request-decode). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### reference-server

**P1 — The build-only view omits PR 12’s shared runtime, native TLS, readiness, and shutdown. App.wire registers services; it does not start workers. “One *sql.DB” is false for memory mode.**

Evidence: [server/internal/runtime/runtime.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/runtime/runtime.go#L35), [server/internal/runtime/runtime_test.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/runtime/runtime_test.go#L1), [server/internal/app/app.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/app.go#L494), [server/cmd/dmserver/main.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/cmd/dmserver/main.go#L1), [server/internal/bench/environment.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/bench/environment.go#L1).

Proposed structure:

- Recast the workflow as configure → Build → listen → serve and run workers → drain. Add runtime entry and shutdown nodes; keep configuration failures branching from validation.
- Replace r5’s App.wire→workers start relationship with runtime → workers, labelled “Run configured worker loops”. Keep route registration as a build-time relationship.
- Add “HTTP or native TLS listener” / “Accept device and admin requests” and “Ordered shutdown” / “Drain HTTP, stop workers, close resources”.

Public context: [Go command: Test packages](https://pkg.go.dev/cmd/go#hdr-Test_packages), [Go Modules Reference](https://go.dev/ref/mod).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#reference-server). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### enrollment-paths

**P1 — The title implies complete enrollment coverage but shows only ADE and account-driven paths. PRs 12/14 add maintained OTA/manual administration, service discovery, stable profiles, and independent HTTPS trust. SCEP/ACME are identity methods, not enrollment types.**

Evidence: [server/internal/app/enroll.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/enroll.go#L508), [server/internal/app/serviceconfig.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/serviceconfig.go#L29), [server/internal/app/ota.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/ota.go#L1), [server/internal/app/adminbench.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/adminbench.go#L1), [mdmprotocol/enroll/ade/handler.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/enroll/ade/handler.go#L136).

Proposed structure:

- Use four lanes: Automated Device Enrollment, account-driven enrollment, profile-based/OTA delivery, and common identity/check-in. Remove the standalone refusal lane; attach outcome notes to the relevant gates.
- Add manual node “Profile-based enrollment” / “Export and install an authorized enrollment profile”; add OTA node “OTA profile delivery” / “SCEP-only, with explicit challenge and signer trust”. OTA first returns identity payloads without MDM, then the MDM profile after identity authentication. The configured reference-server OTA path rejects ACME.
- Add a discovery card explaining /MDMServiceConfig versus /.well-known/com.apple.remotemanagement. Show trust as a prerequisite, not a replacement for signed device identity.

Public context: [Deploying device management enrollment profiles](https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles), [MDM payload](https://developer.apple.com/documentation/devicemanagement/mdm), [Onboarding users with account-driven enrollment](https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment), [MachineInfo](https://developer.apple.com/documentation/devicemanagement/machineinfo).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#enrollment-paths). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### test-harness

**P1 — The diagram invents a unit→fuzz→contract pipeline and omits shared embedded/process scenarios and live evidence. The 95% gate is a threshold, not evidence that the current revision passes it. PR 14 records a failing per-package coverage gate.**

Evidence: [Makefile](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/Makefile#L1), [.github/workflows/go-test.yml](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/.github/workflows/go-test.yml#L1), [scripts/coverage-gate.sh](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/scripts/coverage-gate.sh#L1), [server/internal/bench/catalog.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/bench/catalog.go#L1), [server/acceptance/scenarios_test.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/acceptance/scenarios_test.go#L1), [docs/testing/enrollment-validation.md](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/docs/testing/enrollment-validation.md#L1).

Proposed structure:

- Replace sequential t1 and t2 with independent test lanes that feed coverage where they emit profiles. Fuzz smoke is a separate check, not a prerequisite for storage contracts.
- Add “Process acceptance” / “Run shared scenarios against built dmserver” → coverage (“Acceptance coverage profiles”). Add “Live device evidence” / “Requires real Apple credentials and hardware” as a distinct validation branch, not a simulated CI success.
- Link the bench companion for supervision and fixtures; retain backend contract coverage without implying every live test uses fakes.

Public context: [Go command: Test packages](https://pkg.go.dev/cmd/go#hdr-Test_packages).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#test-harness). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### flow-dep-sync-assign

**P2 — The core flow is sound. Replace legacy DEP-first prose, explain the public-certificate/token exchange, and avoid presenting enrollment-profile assignment as device enrollment itself.**

Evidence: [appleplatformservices/dep/tokenpki.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/appleplatformservices/dep/tokenpki.go#L1), [appleplatformservices/dep/syncer.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/appleplatformservices/dep/syncer.go#L1), [appleplatformservices/dep/assigner.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/appleplatformservices/dep/assigner.go#L1), [server/internal/app/dep.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/dep.go#L1).

Proposed structure:

- Keep token onboarding before the worker flow. Label sync and assignment as recurring reconciliation rather than a single device enrollment sequence.

Public context: [Device assignment](https://developer.apple.com/documentation/devicemanagement/device-assignment).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#flow-dep-sync-assign). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### flow-ade-enrollment

**P0 — The sequence shows web authentication unconditionally after a POST, although only GET with WebAuth configured takes that branch. It also makes the software catalogue return the device-facing HTTP 403 and depicts an OIDC callback as a direct provider→server call.**

Evidence: [mdmprotocol/enroll/ade/handler.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/enroll/ade/handler.go#L136), [mdmprotocol/enroll/ade/softwareupdate.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/enroll/ade/softwareupdate.go#L107), [server/internal/app/enroll.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/enroll.go#L508), [server/internal/app/serviceconfig.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/serviceconfig.go#L29).

Proposed structure:

- After admission and optional update lookup, split into mutually exclusive branches: direct POST (or GET without WebAuth) → profile; GET with WebAuth → browser sign-in → callback → profile.
- Move HTTP 403 update-required response to ade → device. gdmf → ade returns catalogue data only.
- Make the callback device/browser → ade after the identity provider redirects the browser. Add token exchange/verification as an ade ↔ idp exchange.
- Include certificate acquisition between receiving the profile and Authenticate. Add TokenUpdate acknowledgement as the final response.

Public context: [MachineInfo](https://developer.apple.com/documentation/devicemanagement/machineinfo), [Authenticating through web views](https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views), [ErrorCodeSoftwareUpdateRequired](https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired), [Deploying device management enrollment profiles](https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#flow-ade-enrollment). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### flow-account-driven-enrollment

**P1 — The response arrow “reserve, persist, confirm” exposes internal steps as if they were a wire response. Discovery names only mdm-byod, certificate issuance is collapsed into profile delivery, and the sequence stops before TokenUpdate.**

Evidence: [server/internal/app/enroll.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/enroll.go#L508), [mdmprotocol/enroll/accountdriven/handler.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/enroll/accountdriven/handler.go#L1), [mdmprotocol/enroll/accountdriven/hook.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/enroll/accountdriven/hook.go#L1), [mdmprotocol/enroll/discovery/discovery.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/enroll/discovery/discovery.go#L1).

Proposed structure:

- Replace the hook response with an empty successful HTTP response; explain reserve/persist/confirm in a card.
- Add explicit certificate request and return between profile receipt and Authenticate. Add TokenUpdate and its successful acknowledgement after Authenticate.
- Keep apple-as-web and apple-oauth2 as alternatives; do not imply every account enrollment is User Enrollment or has the same bearer requirements.

Public context: [Onboarding users with account-driven enrollment](https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment), [Deploying device management enrollment profiles](https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles), [MDM payload](https://developer.apple.com/documentation/devicemanagement/mdm).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#flow-account-driven-enrollment). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### flow-command-delivery

**P1 — The happy path hides Idle, no-command responses, and completion persistence. PR 14’s replacement InstallProfile is delivered through a separate staged path rather than the ordinary queue shown here.**

Evidence: [server/service/connect.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/connect.go#L39), [server/service/service.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/service.go#L303), [server/internal/app/adminmdm.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/adminmdm.go#L1), [server/internal/app/adminbench.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/adminbench.go#L1), [server/service/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/replacement.go#L40), [storage/storage.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/storage/storage.go#L335).

Proposed structure:

- Add a final core → store message “Store the acknowledged result”. Add a final core → device message “Return next command or empty HTTP 200”.
- Make StoreResult conditional on a non-Idle incoming response. Annotate the main sequence as the ordinary command queue; cross-link controlled replacement.
- Keep APNs asynchronous and explicitly state that APNs acceptance is not command completion.

Public context: [Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device), [Handling NotNow status responses](https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#flow-command-delivery). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### flow-ddm-sync

**P1 — The diagram visually connects devices directly to the engine, omits the wake/poll exchange, and leaves response content implicit. Engine arrows should be logical calls with the transport stated, not public HTTP endpoints.**

Evidence: [server/ddmsync/notifier.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/ddmsync/notifier.go#L1), [mdmprotocol/ddm/serve.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/mdmprotocol/ddm/serve.go#L156), [server/ddmadapter/inproc/inproc.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/ddmadapter/inproc/inproc.go#L1), [server/service/checkin.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/checkin.go#L29).

Proposed structure:

- Replace the core participant with “MDM service and adapters” / “Device-facing /mdm transport”. Route tokens/items/fetch/status from device to core, then forwarded calls core → engine.
- Add notifier → device as a clearly annotated collapsed path “Request an APNs wake (via push notifier)”; add device → core “Poll for command” before delivery. Cross-link the complete command-delivery diagram.
- Show responses: sync token, declaration manifest, changed declaration body, and empty status acknowledgement. The four exchanges are protocol operations, not a fixed compulsory sequence after every change.

Public context: [Integrating declarative management](https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management), [Declarative Management check-in](https://developer.apple.com/documentation/devicemanagement/declarative-management), [Declarations](https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#flow-ddm-sync). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### flow-acme-attestation

**P1 — The sequence omits obtaining a replay nonce and shows certificate download as an unsolicited response after finalization. Binding comparison belongs to the coordinator, not a store RPC. Explain CSR key matching at finalization.**

Evidence: [pki/acme/server.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/pki/acme/server.go#L249), [pki/acme/handlers.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/pki/acme/handlers.go#L579), [pki/acme/attest/attest.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/pki/acme/attest/attest.go#L1), [pki/acme/policy.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/pki/acme/policy.go#L1).

Proposed structure:

- Add device → acme “HEAD /acme/new-nonce” and acme → device “Replay-Nonce” before new-account.
- Change binding from acme → store to an acme self-operation “Compare attested identifiers with the order binding”.
- After signing, add acme → device “Finalized order with certificate URL”, then device → acme “POST-as-GET certificate URL”; only then return the PEM chain.
- Do not require serial and UDID for every binding: validate whichever device identifiers the binding requires, including account-enrollment privacy constraints.

Public context: [ACMECertificate](https://developer.apple.com/documentation/devicemanagement/acmecertificate), [Deploy Managed Device Attestation](https://support.apple.com/en-gb/guide/deployment/dep54e5ac1fd/web), [RFC 8555 — ACME](https://www.rfc-editor.org/rfc/rfc8555).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#flow-acme-attestation). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### flow-scep-issuance

**P1 — The challenge response “derived from the common name” incorrectly generalizes the HMAC strategy to static and one-use credentials. Success and failure are drawn as successive replies; PR 14 adds CSR-bound replacement authorization.**

Evidence: [pki/scep/scep.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/pki/scep/scep.go#L136), [pki/scep/challenge.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/pki/scep/challenge.go#L1), [server/internal/app/security.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/security.go#L1), [server/internal/app/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/replacement.go#L284).

Proposed structure:

- Keep PKCSReq as the illustrated initial issuance path. Label success and signed failure as mutually exclusive branches, with failure leaving challenge/CSR validation before signing.
- Add issuer registration before CertRep for account and replacement identities, scoped to those configured paths.
- Explain renewal separately: accepted existing-identity renewal can bypass challenge verification after certificate-status checks.

Public context: [SCEP](https://developer.apple.com/documentation/devicemanagement/scep), [RFC 8894 — SCEP](https://www.rfc-editor.org/rfc/rfc8894), [Deploying device management enrollment profiles](https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#flow-scep-issuance). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### lifecycle-command

**P0 — The NotNow card implies that only backoff-pending commands are skipped on the current connection, but skipNotNow skips every NotNow command. Error/CommandFormatError, redelivery of sent commands, and clearing from pending/not-now are missing.**

Evidence: [server/service/connect.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/connect.go#L39), [storage/storage.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/storage/storage.go#L335), [storage/inmem/inmem.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/storage/inmem/inmem.go#L284), [server/sqlstore/sqlcommon/store.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/sqlstore/sqlcommon/store.go#L1).

Proposed structure:

- Add sent → sent “Retry delivery before a terminal result”; Next can return sent records again.
- Add pending → cleared and notnow → cleared. Retain sent → cleared, each labelled “Clear or ordinary enrollment reset”.
- Add a distinct command-format-error terminal state, or group both wire statuses under a state labelled “Command failed” with sublabel “Error or CommandFormatError”; choose the grouped state here to keep the diagram compact.
- Label retry “Later poll, backoff elapsed, not skipping NotNow”. Do not draw retry as a timer-driven server push.

Public context: [Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device), [Handling NotNow status responses](https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#lifecycle-command). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

### lifecycle-enrollment

**P0 — The lifecycle conflates accepted ordinary re-enrollment with identity rotation and treats rejected reuse as a persisted enrollment state. It omits controlled replacement and implies checkout permanently prevents enrollment.**

Evidence: [server/service/checkin.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/checkin.go#L29), [server/service/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/replacement.go#L40), [storage/inmem/inmem.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/storage/inmem/inmem.go#L284), [storage/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/storage/replacement.go#L72), [storage/storagetest/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/storage/storagetest/replacement.go#L17).

Proposed structure:

- Keep the main device-channel lifecycle, and label it explicitly. Add enabled → reenrolled and checkedout → authenticated for accepted ordinary Authenticate. Change rotate to “Accepted ordinary Authenticate”, since reset is not restricted to changed certificates.
- Represent reuse denial as a rejected request annotation with no state transition: a refusal leaves an existing working enrollment unchanged.
- Add enabled → enabled self-loop “Controlled replacement committed”; link the companion for pending, failure, cancellation, and expiry.
- Use ordinary lifecycle states for waiting/reset/disabled; do not present a successful re-enrollment as an operational failure. User-channel TokenUpdate creation belongs in a card.

Public context: [Check-in](https://developer.apple.com/documentation/devicemanagement/check-in), [Deploying device management enrollment profiles](https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles), [MDM payload](https://developer.apple.com/documentation/devicemanagement/mdm).

Exact copy: [all affected fields](diagram-review-2026-09-10.copy.md#lifecycle-enrollment). Retain the other authored fields listed in the JSON manifest, except elements explicitly removed by this structural proposal.

## Verification and implementation acceptance

The focused command `go test ./storage/... ./server/service/... ./mdmprotocol/ddm/...` passed during this review. This corroborates the inspected protocol, in-memory storage, and service behaviour; it is not a new SQL fleet, process acceptance, coverage, or live-device validation claim. No new Go tests are needed for this documentation-only proposal.

The review-package check verifies 27 source matches, valid original pointers and old values, all retained-copy decisions, four companion graphs with valid endpoints, repository evidence paths, successful public reference retrievals, and current artifact hashes. Proposed text and topology have not been rendered or Archify-validated as replacement diagrams.

For subsequent diagram implementation:

1. Correct the four P0 diagrams and apply PR-specific coverage additions; keep behavior changes out of production code.
2. Apply the exact copy and reference cards, rebuild guided-view focus IDs where topology changes, and update repository pins to the implementation baseline. Regenerate the diagram catalogue descriptions and reading order.
3. Validate each candidate with Archify showcase checks; for architecture use `--repo-root .`. Require all nine checks with zero composition errors and warnings before delivery.
4. Deliver the frozen candidate, then run visual-check on that exact HTML. Inspect both themes at 1440×900 and 2048×1320 and measure containment at 1440×900, 1600×1000, 1920×1080, and 2048×1320. Check text and toolbar readability as well as bounds.
5. Recheck search, focus, guided views, and relevant export controls. Refresh the system architecture PNGs and artifact receipts after successful delivery. Record semantic review, deterministic checks, browser evidence, and perceptual review separately.

Recommended reading order: system architecture → enrollment methods → command delivery → declarative synchronization → enrollment lifecycle and controlled replacement → MDM/app push → runtime/bench → implementation detail diagrams.
