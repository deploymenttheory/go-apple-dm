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

On the tested physical Mac running macOS 27.0 (26A428), a SigningID-only
`DeniedBinaries` rule also prevented unrelated ad-hoc signed Python and Git from
launching; an unrelated Developer ID app continued to run. Removal restored all
controls. The delivered payload and native policy contained only the requested
deny rule. See the [binary isolation investigation](../testing/app-settings-binary-isolation.md)
before evaluating native enforcement on this build. Schema validation and valid/active
device status do not establish isolated execution control.

See [macOS identity acceptance](../testing/application-identities-macos.md) for the
vendor-artifact installation procedure, native hash comparisons and recorded limits.
