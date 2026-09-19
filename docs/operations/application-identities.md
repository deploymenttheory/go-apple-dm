# Application identity authoring

The reference server discovers the identifiers needed to author existing DDM
app control payloads. Start with an app name or distribution artifact, review
the returned candidates, then populate `ddm.AppSettings` and publish an ordinary
Blueprint. Discovery selects no application or allow/deny policy automatically.

```mermaid
flowchart LR
  Source[App name or uploaded artifact] --> Discovery[Authorized identity discovery]
  Discovery --> Review[Select app and matching identifiers]
  Review --> Payload[Explicit typed DDM payload]
  Payload --> Validate[Validate target compatibility]
  Validate --> Blueprint[Publish and assign Blueprint]
  Blueprint --> Device[Authenticated device DDM sync]
  Device --> Verify[Compare payload, activation and server tokens]
  Verify --> Remove[Unassign and verify removal on next sync]
```

## API

All paths below are relative to `/admin/v1`. Routes are available wherever the
Blueprint administration routes are mounted. Stored principals need the
`discoverApplicationIdentities` action on the system resource. The development
static admin token retains its normal root behavior. Discovery uses existing
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

Fetch `tokens`, then `declaration-items`. Match the returned declarations token,
find the compiled configuration identifier in `Declarations.Configurations`, and
fetch `declaration/configuration/{identifier}`. Check its `Type`, `Identifier`,
`ServerToken` and complete `Payload` against the authored selection. Also fetch
the activation identified by `Compiled.Activations["default"]`; its
`StandardConfigurations` must contain that configuration identifier. A stored
configuration without its applicable activation does not complete this workflow.

The integration test uses the existing simulator's `SyncDDM` to perform that
protocol exchange. It verifies both storage backends, same-name App Store
candidates, a two-app artifact with distinct hashes, all architectures, target
rejection, assignment isolation, unchanged republication, and removal. It makes
no public App Store request and changes no physical device:

```sh
go test ./server/internal/app -run '^TestApplicationSettings' -race -count=1
```

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

macOS binary controls impose a signing restriction independently of the selected
identifier rules. Review the [execution contract and recorded control matrix](../testing/app-settings-binary-isolation.md#protocol-expectation)
before assignment, including its effect on ad-hoc developer tools. Discovery and
schema validation establish neither execution eligibility nor preservation of
every unrelated executable. The physical macOS 27.0 (26A428) run matched the
documented behavior, and removal restored every control. No policy is broadened
or rewritten by the authoring API to compensate for platform restrictions.

See [macOS identity acceptance](../testing/application-identities-macos.md) for the
vendor-artifact installation procedure, native hash comparisons and recorded limits.
