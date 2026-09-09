# Diagrams

Twenty-seven interactive diagrams of this library and the reference server, generated with the
[archify](https://github.com/tt-a1i/archify) skill from the JSON sources in [`src/`](src/).

Each `.html` file opens locally without a build step or network connection. GitHub displays
repository HTML as source, so the links below use [htmlpreview](https://htmlpreview.github.io)
to fetch and render the file from `main`. The root [README](../../README.md) embeds a rendered
architecture image.

The viewer provides search, focus, relationship tracing, guided views, light/dark themes and
image/video export. The generated HTML includes the viewer runtime; edit authored diagram
content in JSON and regenerate rather than modifying HTML.

Architecture sources pin a repository revision and component file paths. Validation checks
those paths against the local Git objects. Other diagram types describe protocol relationships
without per-component source pins. All diagrams are reviewed against the architecture guide
and the cited package contracts.

## Structure

| Diagram | What it shows |
|---|---|
| [system-architecture](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/system-architecture.html) | The high-level design: Apple's services, the reference server's roles, storage, and the admin plane |
| [package-layering](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/package-layering.html) | The nine tiers, the direction a tier may import in, and where the module splits |
| [storage-contract](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/storage-contract.html) | All eight interfaces `storage.Store` composes, and which caller depends on each |
| [storage-backends](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/storage-backends.html) | Shared SQL implementation, backend dialects and column sealing |
| [service-layer](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/service-layer.html) | Certificate middleware, hooks, `DMHandler`, `UserVerifier` and events |
| [checkin-dispatch](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/checkin-dispatch.html) | All nine check-in messages, split into state changes and answers, and how each is refused |
| [ddm-engine](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/ddm-engine.html) | The engine, the notifier, and the change rows that decouple them |
| [ddm-serve](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/ddm-serve.html) | The four endpoint operations and the two refusals `ParseEndpoint` can produce |
| [acme-internals](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/acme-internals.html) | `jose`, identifiers, orders, attestation, policy, and the RFC 7807 problem every refusal becomes |
| [push](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/push.html) | `pushnotify.Notifier` through coalescing to APNs, and where the push certificate comes from |
| [apple-service-clients](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/apple-service-clients.html) | The DEP, Business Manager and software-lookup clients, and their three auth schemes |
| [admin-plane](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/admin-plane.html) | `dmctl` to the admin API to Cedar, and how credentials are referenced |
| [split-deployment](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/split-deployment.html) | The `mdm` and `ddm` roles either side of an HMAC-signed hop |

## Flows

| Diagram | Type | What it shows |
|---|---|---|
| [schema-generation](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/schema-generation.html) | data flow | Apple's YAML becoming typed Go, and the rename guard that protects callers |
| [request-decode](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/request-decode.html) | data flow | A device request becoming a typed message, and where the identity certificate comes from |
| [reference-server](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/reference-server.html) | workflow | How `server/internal/app` builds and serves, and what fails at build rather than at runtime |
| [enrollment-paths](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/enrollment-paths.html) | workflow | Automated and account-driven enrollment, and the gates that refuse before a profile is built |
| [test-harness](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/test-harness.html) | workflow | The test pipeline from unit tests to the coverage gate |
| [flow-dep-sync-assign](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-dep-sync-assign.html) | workflow | The token PKI exchange, then the sync and assignment workers |
| [flow-ade-enrollment](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-ade-enrollment.html) | sequence | Signed `MachineInfo`, the update gate, the OIDC web view, then check-in |
| [flow-account-driven-enrollment](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-account-driven-enrollment.html) | sequence | Discovery, the `401` Bearer challenge, web authentication, then certificate association and bearer authentication |
| [flow-command-delivery](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-command-delivery.html) | sequence | Target validation, queueing, APNs wake and the connect exchange |
| [flow-ddm-sync](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-ddm-sync.html) | sequence | Declaration changes, synchronization commands, declaration retrieval and status reporting |
| [flow-acme-attestation](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-acme-attestation.html) | sequence | The ACME order, `device-attest-01`, and the four checks before a certificate is issued |
| [flow-scep-issuance](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/flow-scep-issuance.html) | sequence | `GetCACaps`, `GetCACert`, and a `PKIOperation` gated by a challenge |
| [lifecycle-command](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/lifecycle-command.html) | lifecycle | `pending` to `sent` to a terminal outcome, with the `NotNow` retry |
| [lifecycle-enrollment](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/lifecycle-enrollment.html) | lifecycle | Authenticate, TokenUpdate, re-enrollment, and CheckOut |

## Regenerating

Edit the sources in [`src/`](src/) and regenerate the HTML with the installed `archify` skill.
Replace `<archify>` with the skill directory:

```bash
node <archify>/bin/archify.mjs deliver <type> docs/diagrams/src/<name>.<type>.json docs/diagrams/<name>.html \
  --quality showcase --json
node <archify>/bin/archify.mjs visual-check docs/diagrams/<name>.html --json
```

`<type>` is the second extension of the source file: `architecture`, `workflow`, `sequence`,
`dataflow`, or `lifecycle`. Architecture diagrams additionally need `--repo-root .`, because they
carry git-verified source pins: `meta.repository` names a commit and each component cites real
file paths, which the renderer checks against the local git objects. A wrong path, line range or
commit fails validation.

Validate with `--quality showcase`, then run `visual-check` on the delivered HTML. Inspect both
themes and the default READ view. Browser checks measure 1440×900, 1600×1000, 1920×1080 and
2048×1320. Source validation, browser evidence and visual review are separate checks.

Layout constraints:
an architecture canvas must satisfy `height / width <= 0.59`, and a workflow compiles to at most
four lanes and five columns.

The two PNGs beside `system-architecture.html` are the images the root README embeds. They are
cropped from a `visual-check` capture; regenerate them the same way after changing that diagram.

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
