# Diagrams

Thirty-one interactive diagrams explain the library and reference server. They are generated
with [Archify](https://github.com/tt-a1i/archify) from the JSON sources in [`src/`](src/).
The latest revision: September 2026 revision incorporates PRs [12](https://github.com/deploymenttheory/go-apple-dm/pull/12)
and [14](https://github.com/deploymenttheory/go-apple-dm/pull/14), checked against commit
`ff85958196cb013e8e14c1055f067319b07e3299`.

Open a local `.html` file in a browser; it needs no build step or network connection. The
**online** links below render the version on `main`, so local changes appear there only after
publication. Each diagram includes explanatory cards and clickable public documentation.
Following a documentation link requires a connection.

All diagrams include search, focus, relationship tracing, available guided views, light/dark
themes, and exports. They share an [eight-purpose colour legend](colours.md), with green,
orange, and red reserved for success, warnings or deferral, and failure. Each diagram displays
the relevant legend entries.

## Where to start

1. Read [system architecture](system-architecture.html) for the component map, then
   [enrollment paths](enrollment-paths.html) for how a device joins management.
2. Follow [command delivery](flow-command-delivery.html) and
   [declarative synchronization](flow-ddm-sync.html) for ongoing management.
3. Use [profile replacement](flow-enrollment-profile-replacement.html),
   [APNs credentials](apns-certificate-workflows.html), and
   [app notifications](flow-app-notification-delivery.html) for the newer capabilities.
4. Read the [server runtime](reference-server.html) and [bench](reference-server-bench.html)
   before working on integration scenarios or live-device evidence.

## Structure

| Diagram | What it explains |
|---|---|
| [system-architecture](system-architecture.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/system-architecture.html) | Start here: Apple services, device traffic, the shared server runtime, storage, and separate MDM and app push paths. |
| [package-layering](package-layering.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/package-layering.html) | Conceptual package tiers, the module boundary, and the specific ADE software-catalogue import exception. |
| [storage-contract](storage-contract.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/storage-contract.html) | The eight interfaces composed by storage.Store, their callers, and the optional ReplacementStore extension. |
| [storage-backends](storage-backends.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/storage-backends.html) | Memory and SQL backends, shared pools, conditional encryption, and the separate app-push state namespace. |
| [service-layer](service-layer.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/service-layer.html) | How transport, identity checks, hooks, replacement handling, declarative management, and asynchronous audit fit together. |
| [checkin-dispatch](checkin-dispatch.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/checkin-dispatch.html) | Nine independent check-in handlers and their responsibilities; arrows select a handler rather than prescribe a message sequence. |
| [ddm-engine](ddm-engine.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/ddm-engine.html) | Desired declarations, snapshots, change records, and notifications, with device-side activation and replacement-safe cleanup explained. |
| [ddm-serve](ddm-serve.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/ddm-serve.html) | The four declarative operations carried inside check-in, including successful responses and the point at which each error is detected. |
| [acme-internals](acme-internals.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/acme-internals.html) | ACME coordination, nonce consumption, attestation and admission checks, CSR finalization, and certificate issuance. |
| [push](push.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/push.html) | The MDM wake path from a coalesced request to APNs and the device, including invalid-token events and certificate reloads. |
| [apple-service-clients](apple-service-clients.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/apple-service-clients.html) | Device-assignment, Apple Business Manager, and software-catalogue clients, with their distinct authentication and reconciliation behavior. |
| [admin-plane](admin-plane.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/admin-plane.html) | Static-root and stored-principal authorization, current administration capabilities, and CLI credential handling. |
| [split-deployment](split-deployment.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/split-deployment.html) | In-process declarative management or separate MDM/DDM roles connected through the signed internal adapter. |
| [reference-server-bench](reference-server-bench.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/reference-server-bench.html) | Shared runtime supervision, embedded and process scenarios, fixture boundaries, and separate live-device evidence. |

## Flows and lifecycles

| Diagram | What it explains |
|---|---|
| [schema-generation](schema-generation.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/schema-generation.html) | Apple schemas becoming generated Go types, support tables, provenance, and verification artifacts. |
| [request-decode](request-decode.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/request-decode.html) | Typed messages and three alternative certificate sources feeding service authorization; extraction is distinct from enrollment approval. |
| [reference-server](reference-server.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/reference-server.html) | Configuration and build, HTTP or native TLS serving, worker startup, readiness, and ordered shutdown. |
| [enrollment-paths](enrollment-paths.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/enrollment-paths.html) | Automated Device Enrollment, account-driven enrollment, and profile-based/OTA delivery converging on identity and check-in. |
| [apns-certificate-workflows](apns-certificate-workflows.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/apns-certificate-workflows.html) | Separate operator workflows for MDM push certificates and app notification credentials, including import and connection retirement. |
| [test-harness](test-harness.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/test-harness.html) | Independent unit, contract, fuzz, embedded, process, and live checks; what contributes coverage and what requires device evidence. |
| [flow-dep-sync-assign](flow-dep-sync-assign.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-dep-sync-assign.html) | Device-assignment server-token setup followed by recurring inventory synchronization, assignment, and readback. |
| [flow-ade-enrollment](flow-ade-enrollment.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-ade-enrollment.html) | Conditional admission and web authentication, profile delivery, identity issuance, Authenticate, and TokenUpdate. |
| [flow-account-driven-enrollment](flow-account-driven-enrollment.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-account-driven-enrollment.html) | Discovery, alternative authentication methods, profile delivery, certificate association, and completed check-in. |
| [flow-command-delivery](flow-command-delivery.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-command-delivery.html) | The ordinary queue, asynchronous MDM wake, device polling, result persistence, and empty or next-command responses. |
| [flow-ddm-sync](flow-ddm-sync.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-ddm-sync.html) | Change notification and device polling followed by declarative operations through the MDM service and its adapters. |
| [flow-acme-attestation](flow-acme-attestation.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-acme-attestation.html) | Replay nonce acquisition, device attestation, identifier binding, CSR key matching, and explicit certificate retrieval. |
| [flow-scep-issuance](flow-scep-issuance.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-scep-issuance.html) | Initial SCEP issuance with mutually exclusive success and failure outcomes, plus renewal and enrollment-association notes. |
| [flow-enrollment-profile-replacement](flow-enrollment-profile-replacement.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-enrollment-profile-replacement.html) | Prepare and deliver a controlled replacement, stage candidate identity, and commit only after all required evidence arrives. |
| [flow-app-notification-delivery](flow-app-notification-delivery.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-app-notification-delivery.html) | Explicit app topic, environment, and token inputs; APNs acceptance versus app receipt; optional bench correlation. |
| [lifecycle-command](lifecycle-command.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/lifecycle-command.html) | Pending, sent, deferred, failed, acknowledged, and cleared commands, including redelivery and NotNow polling rules. |
| [lifecycle-enrollment](lifecycle-enrollment.html) · [online](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/lifecycle-enrollment.html) | Device-channel activation, ordinary reset, checkout, and controlled replacement without disrupting the working enrollment. |

## Regenerating and reviewing

Edit JSON, then use the repository's [reading profile](../../scripts/diagrams/README.md).
It stages an isolated copy of Archify 2.17 before rendering, so validation sees the same
larger typography and layout as the delivered HTML. It leaves the installed skill untouched.
The sources use this profile's explicit purpose-colour extension. Regenerate through the
wrapper to retain the shared colours, legends, larger text, and clickable references.

```bash
node scripts/diagrams/archify.mjs validate <type> docs/diagrams/src/<name>.<type>.json \
  --quality showcase --json
node scripts/diagrams/archify.mjs deliver <type> docs/diagrams/src/<name>.<type>.json docs/diagrams/<name>.html \
  --quality showcase --json
node scripts/diagrams/archify.mjs visual-check docs/diagrams/<name>.html --json
```

The type is `architecture`, `workflow`, `sequence`, `dataflow`, or `lifecycle`. Add
`--repo-root .` to architecture validation and delivery: those sources pin component paths to
a Git revision. Other diagram types use the code evidence in the
[review](../reviews/diagram-review-2026-09-10.md).

Require all nine showcase artifact checks, with zero errors and warnings. Inspect the exact
delivered HTML at 1440×900, 1600×1000, 1920×1080, and 2048×1320, including light and dark
screenshots at the smallest and largest sizes. Keep horizontal containment, readable labels,
clear routes, and accessible explanatory cards. Vertical page scrolling is intentional.
Upstream `visual-check` still treats vertical scrolling as a containment failure; retain that
raw result and assess it against this reading policy rather than claiming an upstream pass.
Source validation, browser measurements, and visual inspection are separate evidence.

The [implementation record](../reviews/diagram-implementation-2026-09-10.md) records the final
artifact hashes, validation, browser measurements, and visual review. The earlier review and
preview records are historical evidence, not regeneration inputs.

The two `system-architecture.*.png` files are the images embedded by the root
[README](../../README.md). After changing the overview, use the viewer's full-diagram PNG
export in both themes. Canonical export excludes navigation, focus, and other viewer controls.

## Sources

These diagrams describe this repository's own code. The protocol facts behind them come from
Apple's documentation, which each package cites in the `# References` section of its `doc.go`:

- [Device Management](https://developer.apple.com/documentation/devicemanagement) — the root
- [Check-in](https://developer.apple.com/documentation/devicemanagement/check-in) and
  [Commands and queries](https://developer.apple.com/documentation/devicemanagement/commands-and-queries)
- [Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device)
  and [Handling NotNow status responses](https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses)
- [Integrating declarative management](https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management),
  [DeclarativeManagementRequest](https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest),
  [Declarations](https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations),
  [Status items](https://developer.apple.com/documentation/devicemanagement/status-items)
- [Setting up push notifications](https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers)
  and [Dealing with inactive managed devices and invalid push tokens](https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens)
- [SCEP](https://developer.apple.com/documentation/devicemanagement/scep),
  [Managing certificates](https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices),
  [ACMECertificate](https://developer.apple.com/documentation/devicemanagement/acmecertificate)
- [MachineInfo](https://developer.apple.com/documentation/devicemanagement/machineinfo),
  [Authenticating through web views](https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views),
  [ErrorCodeSoftwareUpdateRequired](https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired)
- [Onboarding users with account-driven enrollment](https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment)
  and its [simple](https://developer.apple.com/documentation/devicemanagement/implementing-the-simple-authentication-account-driven-enrollment-flow)
  and [OAuth 2](https://developer.apple.com/documentation/devicemanagement/implementing-the-oauth2-authentication-account-driven-enrollment-flow) flows
- [Device assignment](https://developer.apple.com/documentation/devicemanagement/device-assignment),
  [Authenticating for automated device enrollment](https://developer.apple.com/documentation/devicemanagement/authenticating-for-automated-device-enrollment),
  [Fetch devices](https://developer.apple.com/documentation/devicemanagement/fetch-devices),
  [Sync devices](https://developer.apple.com/documentation/devicemanagement/sync-devices),
  [Assign profile](https://developer.apple.com/documentation/devicemanagement/assign-profile)
- [Apple Business Manager API](https://developer.apple.com/documentation/applebusinessapi) and
  [its OAuth](https://developer.apple.com/documentation/apple-school-and-business-manager-api/implementing-oauth-for-the-apple-school-manager-and-apple-business-api)
- [RFC 8555](https://www.rfc-editor.org/rfc/rfc8555) (ACME) and
  [RFC 8785](https://www.rfc-editor.org/rfc/rfc8785) (JSON Canonicalization Scheme)

The design decisions the diagrams reflect are recorded in
[`docs/research/decisions/`](../research/decisions/README.md).

The [shared-runtime and bench flow](../architecture.md#shared-reference-server-runtime-and-scenarios)
connects the component views above to `dmctl bench`, process acceptance, and interface contracts.
Fixture controllers are outside the production administration plane.
