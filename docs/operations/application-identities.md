# Application identity authoring

The reference server discovers the identifiers needed to author existing DDM
app control payloads. Start with an app name or distribution artifact, review
the returned candidates, then populate `ddm.AppSettings` and publish an ordinary
Blueprint. Discovery selects no application or allow/deny policy automatically.

```mermaid
flowchart LR
  Source[App name or artifact file] --> CLI[dmctl app-identities]
  CLI --> Discovery[Authorized server identity discovery]
  Discovery --> Review[Select app and matching identifiers]
  Review --> Payload[Explicit typed DDM payload]
  Payload --> Validate[Validate target compatibility]
  Validate --> Blueprint[Publish and assign Blueprint]
  Blueprint --> Device[Authenticated device DDM sync]
  Device --> Verify[Compare payload, activation and server tokens]
  Verify --> Remove[Unassign and verify removal on next sync]
```

## CLI

`dmctl app-identities` uses the existing server connection, context, token
reference and CA settings. Public App Store searches and artifact inspection run
on the reference server. All output modes preserve the discovery response,
including candidate metadata, artifact provenance and incomplete-report issues.
Discovery selects no candidate, constructs no policy and publishes no Blueprint.

```sh
export DMCTL_SERVER="https://your-mdm-server"
export DMCTL_TOKEN="@/protected/operator-token"

# Search an explicit storefront and platform category.
dmctl app-identities public-app-store search \
  -term "Example" -country GB -entity iPadSoftware -output json

# Resolve the App Store ID selected after reviewing the search results.
dmctl app-identities public-app-store lookup 123 \
  -country GB -entity iPadSoftware -output json

# Search or resolve an Apple-bundled iPhone/iPad application.
dmctl app-identities apple search -term Safari
dmctl app-identities apple lookup com.apple.mobilesafari

# Stream a local artifact to the server using the same authenticated client.
dmctl app-identities inspect -file /path/to/application.dmg \
  -timeout 3m -output json > identity.json

# A pipeline can supply artifact bytes on stdin.
dmctl app-identities inspect -file - -timeout 3m -output json < application.zip
```

Public App Store search accepts optional `-developer` and `-limit` (1–200; zero
uses the server default). Discovery has no cursor pagination, so catalogue
commands reject `-all`. An Apple catalogue search without `-term` returns the
bundled catalogue. Artifact inspection requires an explicit file or `-file -`;
it streams bytes without loading the complete upload into CLI memory. Use the
normal `-timeout` flag to allow for upload and inspection; the server retains
its own inspection and size limits. Incomplete inspection remains a report to
review, not a selected application or an automatically usable policy.

After selecting the application and matching scope and authoring
`app-controls.json` with the [Go helpers](#complete-reference-server-workflow),
use the ordinary Blueprint commands:

```sh
dmctl blueprints validate -file app-controls.json \
  -target macos:27.0,channel=device,supervised
dmctl blueprints publish -file app-controls.json
dmctl blueprints assign app-controls DEVICE_ID
dmctl enrollments status values device DEVICE_ID \
  -prefix management.declarations -all -output json
dmctl blueprints unassign app-controls DEVICE_ID
```

Use the enrollment's actual OS, version, channel and capabilities in `-target`.
Omitting it retains structural validation. The flag uses the same target syntax
as `dmctl explain` and `dmctl profile lint`, and applies only to
`blueprints validate`. App Store or artifact discovery and schema validation do
not establish native policy enforcement; see the delivery checks below.

## API

All paths below are relative to `/admin/v1`. Routes are available wherever the
Blueprint administration routes are mounted. Stored principals, including root, need the
`discoverApplicationIdentities` action on the system resource. Discovery uses existing
admin quotas and audit handling; its POST does not publish declarations or wake
devices. Publication requires its separate existing permissions.

| Method and path | Input and result |
| --- | --- |
| `GET /authoring/app-identities/public-app-store` | Required `term`, `country` and `entity`; optional `developer` and `limit`. Returns `{ "items": [...] }`. |
| `GET /authoring/app-identities/public-app-store/{id}` | Numeric App Store ID, required `country` and `entity`. Returns a public listing. |
| `GET /authoring/app-identities/apple` | Optional `term`; returns `items`, `sourceURL` and `reviewedOn`. |
| `GET /authoring/app-identities/apple/{bundleID}` | Exact bundle ID; returns `app`, `sourceURL` and `reviewedOn`. |
| `POST /authoring/app-identities/artifacts` | Raw artifact bytes with `Content-Type: application/octet-stream`; returns an artifact report. |

Public App Store entities are `software`, `iPadSoftware` and `macSoftware`.
Storefront and entity are explicit. Developer filtering is local and does not
guarantee a complete catalogue. Empty search results succeed; a missing lookup
returns 404. Upstream throttling returns 429 with bounded `Retry-After`; other
upstream failures return 502, deadlines 504. Apple's bundled catalogue covers
iPhone and iPad apps and is not a macOS identifier catalogue.

Artifacts may be PKG, APFS or HFS+/HFSX UDIF DMG, ZIP or Mach-O. Content identifies
the outer format; nested packages and named DMG/ZIP containers are inspected.
Multiple apps remain separate candidates. Each report records SHA-256, byte
size, format, `complete`, `applications` and any `issues`. Locations are relative
to the containers and must not be used as `PathPrefix` installation rules.

The server reads artifacts with Go libraries on any supported host platform.
It does not execute installers or mount disk images. Scripts make the report
incomplete because they can change or download installed content. Bundle reads
cover the main executable, not embedded helpers. Unsupported filesystems,
encrypted images and PKG hard links are rejected.

Signing IDs and team IDs are observations. Portable results explicitly report
`not-checked` integrity and `unknown` signing category. Every architecture and
supported code directory is retained. An architecture's convenience `cdhash` is
empty when multiple code directories exist: select from `codeDirectories`
explicitly. A hash rule must account for each architecture, and app updates can
change its hashes. Signature verification, notarization and Gatekeeper acceptance
are separate operations.

## Complete reference-server workflow

The maintained [Go examples](../../server/internal/app/applicationauthoring_example_test.go)
use the reference server's HTTP API with existing typed payloads. They are
compile-only examples for a configured server; substitute your server URL,
administrator credential, selected application, target and enrollment ID.
`TestApplicationSettingsWorkflow` in the
[integration test](../../server/internal/app/applicationauthoring_workflow_test.go)
executes the same selection and publication helpers against the assembled server,
then checks delivery through certificate-authenticated device check-ins.

1. Discover candidates using an explicit App Store storefront and entity, or
   upload an artifact. Review names, developers, platform metadata, artifact
   SHA-256 and inspection issues. Search results can contain similar names;
   an artifact can contain several apps.
2. Select the exact App Store ID or artifact candidate `location`. Choose the
   policy and matching scope explicitly. The App Store example authors
   `DeniedApps` for the selected bundle ID. The macOS example authors
   `DeniedBinaries` for all code-directory hashes of the selected build, across
   architectures. It stops on incomplete reports or candidates without hashes.
3. Build `ddm.AppSettings`, wrap it with `blueprint.NewDeclaration`, then validate
   the Blueprint for the intended enrollment's actual target. The examples use
   supervised OS 27 fixtures; version support remains schema-driven.
4. Publish the explicit Blueprint and retain its `Revision` and
   `Compiled.Identifiers["applications"]`. Assign it to the chosen enrollment
   separately. Existing Blueprint updates require `If-Match` with the current
   revision. Publication alone does not deliver a policy to any device.
5. On the device's next DDM sync, verify the configuration and activation as
   described below. Selected identifiers persist in the Blueprint; delivery and
   unchanged republication require neither discovery nor the uploaded file.
6. Unassign the Blueprint, sync again and verify both declarations leave the
   device manifest. Delete the Blueprint with its current revision when it is
   no longer needed.

The author can choose other scopes using the same typed payload:

| Matching scope | Explicit payload fields | Consequence |
| --- | --- | --- |
| iPhone/iPad application identity | `AllowedApps` or `DeniedApps` bundle IDs | Matches the bundle ID across releases; does not install or uninstall the app. |
| Specific macOS build | One `AllowedBinaries` or `DeniedBinaries` entry per selected `CDHash` | Covers the selected code directories; updates can require new hashes. |
| macOS signing identity | `SigningID`, optionally narrowed with the observed `TeamID` in the same entry | Matches those identifiers across builds; review both values before choosing this broader scope. |

Fields within one binary entry must all match. Do not turn an artifact-relative
location into `PathPrefix`, or infer `SigningState` from portable inspection.
The macOS signing restriction described below applies independently of these
identifier choices.

### Verify device-facing delivery

An admin `GET /blueprints/{id}` verifies stored authoring state. To verify delivery,
the enrolled device uses Apple's `DeclarativeManagement` check-in at `/mdm` with
its device identity. An admin bearer token is not a device credential.

Use tokens from the `DeclarativeManagement` command's `Data` when present, or fetch
`tokens` when absent. An unchanged declarations token needs no synchronization.
For a changed token, fetch `declaration-items`. Match the declarations token,
find the compiled configuration identifier in `Declarations.Configurations`, and
fetch `declaration/configuration/{identifier}`. Check its `Type`, `Identifier`,
`ServerToken` and complete `Payload` against the authored selection. Also fetch
the activation identified by `Compiled.Activations["default"]`; its
`StandardConfigurations` must contain that configuration identifier. A stored
configuration without its applicable activation does not complete this workflow.
Apple defines this flow in [DDM integration](https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management).

The integration test uses the existing simulator's `SyncDDM` to perform that
protocol exchange. It verifies both storage backends, same-name App Store
candidates, a two-app artifact with distinct hashes, all architectures, target
rejection, assignment isolation, unchanged republication, and removal. It makes
no public App Store request and changes no physical device:

```sh
go test ./server/internal/app -run '^TestApplicationSettings' -race -count=1
go test ./server/internal/dmctl -run '^TestApplicationIdentityCLIWorkflow$' -race -count=1
```

The CLI workflow test uses `dmctl` for identity discovery, target validation,
publication, assignment and removal against the assembled reference server. A
certificate-authenticated simulator fetches the resulting configuration and
activation. It covers both App Store identity and streamed Mach-O inspection.

This check proves authoring and protocol delivery. Native acceptance, application
visibility and binary execution behavior require device status and on-device
observation; simulator delivery does not establish those outcomes. The owned
Mach-O fixture is ad-hoc signed and is used only to verify inspection and payload
preservation, not as an example of executable eligibility under binary controls.

## Limits and storage

Only one artifact upload/inspection runs at a time per server process. Additional
uploads receive 503 with `Retry-After: 5`. Invalid input returns 400, size or
expansion limits 413, unsupported formats 415 and inspection deadlines 504.
Partial candidate failures return a successful report with `complete: false`.
Container or I/O failures return no report and do not expose scratch paths.

Defaults are 512 MiB per upload/file, 2 GiB expanded data/logical image, 100,000
entries, 128 candidates/issues, four nested containers and a two-minute
cooperative inspection deadline. `app.Config.ApplicationIdentities.Artifacts`
configures these values and the scratch directory in the reference server wiring. Private
temporary uploads and extracted files are removed when the request completes.
No artifact, discovery record or download URL is persisted. Configure deployment
memory/disk limits separately; parser limits are not a process memory guarantee.

## Validate and publish

Populate the existing `AllowedApps`, `DeniedApps`, `AllowedBinaries` or
`DeniedBinaries` fields with the author's selected identifiers. The
[Go authoring examples](../../devicemanagement/utility/README.md) show typed payload
construction. Discovery results are not Blueprint source objects.

`POST /blueprints/validate` accepts an optional target query:

```text
?os=macOS&version=27&channel=device&supervised=true
```

When present, `os`, `version` and `channel` are required. Optional booleans are
`supervised`, `sharedIPad`, `userEnrollment`, `dep` and `userApproved`. Unknown or
duplicate parameters are rejected. With no query, existing structural validation
is preserved. Target compatibility comes from the generated schema, including
future versions; the server does not choose a fixed operating-system release.

Publish the explicit source using the existing Blueprint API. Publication and
republication require no App Store connection or retained uploaded artifact.
Application installation continues to use the existing MDM/DDM mechanisms; a
discovery response or successful publication does not prove device installation.

## Native artifact verification

Use the [artifact inspector](../../devicemanagement/utility/appartifact) to discover
candidate identities, then verify the selected artifact on a designated Mac:

1. Record the server revision, target OS/build, original vendor URL, artifact version,
   SHA-256 and size. Retain exact bytes privately; do not commit vendor binaries.
2. Upload the original PKG, DMG or other supported artifact. Check its reported
   format, completeness, bundle identity and every executable architecture.
3. Install those exact bytes using the intended distribution method. Verify the
   application exists at its installed path; a command acknowledgment, download or
   installer receipt does not by itself prove successful installation. Record a
   native installer control separately from MDM delivery.
4. Compare the installed `Info.plist` and run
   `codesign --verify --strict --all-architectures`. Inspect each architecture with
   `codesign -d --verbose=4 --arch ARCH`, comparing SigningID, TeamID and CDHash to
   the portable report. Apple's [code-signing hash explanation](https://developer.apple.com/documentation/technotes/tn3126-inside-code-signing-hashes)
   distinguishes full code-directory hashes from CDHash and explains per-architecture
   identities; preserve all supported code directories rather than selecting one
   convenience field blindly.
5. Validate a typed payload for the intended target, publish an unassigned Blueprint,
   repeat with its current revision to check idempotence, then delete it. Identity
   inspection does not require assigning execution restrictions to the device.

Installed-app MDM inventory requires the corresponding enrollment access right.
Portable inspection reports signing metadata without validating signature integrity,
notarization or execution eligibility. These independent native checks do not imply
that the server has performed them. See Apple's [InstalledApplicationList command](https://developer.apple.com/documentation/devicemanagement/installedapplicationlistcommand).

## Binary execution controls

Apple's [deployment contract](https://support.apple.com/guide/deployment/allow-and-deny-apps-and-binaries-dep001044b08/1/web/1.0)
requires eligible signing categories when macOS binary controls are enabled.
Unsigned, ad-hoc and development-signed executables can be denied independently
of the chosen identifiers, including when an explicit binary list is empty.
Signature integrity, signing category and execution eligibility are separate facts.

An [AppSettings binary entry](https://developer.apple.com/documentation/devicemanagement/appsettingsallowed_binaryidentifierobject)
combines all supplied matching fields. `SigningState` qualifies the rule; it does
not disable the platform's baseline signing restriction. The server preserves the
authored payload and does not introduce an allow list or broaden a deny rule.

The [SigningID-only example](../testing/fixtures/app-settings-signing-id-deny.json)
illustrates a narrow rule. Replace its target identifier with that of a disposable,
verified Developer ID signed application before testing. For a supervised target
whose schema supports binary controls:

1. Establish baseline execution of the matching target, an unrelated Developer ID
   app, an Apple system executable and separate ad-hoc controls. Inspect signing
   metadata independently.
2. Arm an independent native cleanup watchdog before assignment. It must remove only
   the test assignment and notify the device using credentials stored privately.
   Validate that removal transport before the test; an interpreter affected by the
   restriction cannot be the only cleanup mechanism.
3. Assign the configuration and its activation for a bounded test window. Require
   valid/active device status and match the canonical payload and server token.
   Check actual execution and native denial events; `open` returning zero alone
   does not establish that an app launched.
4. Expect the matching target and ad-hoc controls to be denied, while unrelated
   eligible signed controls run. Remove the assignment in unconditional cleanup,
   verify declaration removal and rerun every baseline control. Remove the policy
   rather than replacing it with an empty binary list.

A physical macOS 27.0 (26A428) deny-mode observation supports that distinction.
It is not validation of the current server revision or every platform variant.
Empty-list, unsigned/development-signed controls, allow mode and managed-app
exceptions need separate native evidence. Discovery and simulator delivery do not
establish native enforcement or successful application installation.

[Delivery regressions](../../devicemanagement/mdmprotocol/ddm/app_settings_delivery_test.go)
cover SigningID-only, CDHash-only, combined identifiers/path and explicit SigningState
payloads, including field preservation and declaration tokens. Run them with:

```sh
go test -race ./devicemanagement/mdmprotocol/ddm ./devicemanagement/mdmprotocol/ddm/blueprint
```
