# Blueprints

The local Blueprint authoring format groups Apple declarations with source identifiers
and activation conditions. Publish its complete desired contents, then assign it
to device or user enrollments. Native DDM configurations, supporting assets and
legacy configuration profiles can share a Blueprint.

The library's `devicemanagement/mdmprotocol/ddm/blueprint` package compiles this
source. The reference server adds persistence, optimistic revisions, admin
authorization and `dmctl` commands. The independent
`server/configurationprofile` package stores and serves configuration profiles. These local Blueprints are
separate from Apple-hosted Blueprint resources accessed through the Apple Business
client in `appleplatformservices/axm`. Apple documents the hosted resource in
[Create a Blueprint](https://developer.apple.com/documentation/applebusinessapi/create-a-blueprint).
The local convenience format below is not the Apple Business API request schema
or a DDM declaration type.

For app controls, use [application identity discovery](application-identities.md)
to select identifiers before constructing the explicit payload. The validation
API accepts target OS, version, channel and enrollment capabilities.
The [complete App Settings workflow](application-identities.md#complete-reference-server-workflow)
connects discovery and explicit matching choices to publication, assignment,
device-facing declaration verification and removal, with maintained Go examples
and an integration test against both reference-server storage backends.

## Author and publish

Save this as `engineering.json`:

```json
{
  "Identifier": "engineering",
  "Name": "Engineering Macs",
  "Declarations": [
    {
      "Identifier": "storage",
      "Type": "com.apple.configuration.diskmanagement.settings",
      "Payload": {"Restrictions": {"ExternalStorage": "Disallowed"}}
    },
    {
      "Identifier": "math",
      "Type": "com.apple.configuration.math.settings",
      "Payload": {"Calculator": {"ScientificMode": {"Enabled": false}}}
    }
  ],
  "Activations": [
    {"Identifier": "baseline", "StandardConfigurations": ["storage", "math"]}
  ]
}
```

```sh
dmctl blueprints validate -file engineering.json
dmctl blueprints publish -file engineering.json
dmctl blueprints assign engineering DEVICE_ID
dmctl blueprints get engineering
```

Publishing returns a `Revision`. Supply that revision on every update:

```sh
dmctl blueprints publish -file engineering.json -revision CURRENT_REVISION
dmctl blueprints unassign engineering DEVICE_ID
dmctl blueprints delete -revision CURRENT_REVISION engineering
```

Updates replace the entire declaration and activation list and preserve existing
assignments. Omitted declarations leave the backing set. An empty Blueprint clears
its contents. Stale or missing update revisions return HTTP 409; reread and resolve
the conflicting edit. An identical publication using the current revision keeps
the revision and queues no additional DDM work. Deleting a Blueprint removes its
assignments. Retained declaration versions and uploaded profiles are not deleted.

Source `Identifier` values are stable: changing one creates a different device
declaration identifier. Display names can change independently. Identifiers are
derived from the Blueprint identifier, declaration kind and source identifier; two
Blueprints therefore have independent copies of a declaration. Low-level admin
declaration and set mutation routes reject the namespaces owned by this compiler.

## Activations and references

With no `Activations`, the compiler creates an unconditional activation for all
configuration declarations. Explicit activations must collectively reference every
configuration. Multiple activations may reference the same configuration.
Add `Predicate` to an activation to use the library's supported Apple predicate
syntax; the device evaluates it. `StandardConfigurations` and `Predicate` map to
[Apple’s ActivationSimple fields](https://developer.apple.com/documentation/devicemanagement/activationsimple). Assignment and activation are separate decisions.

Asset-reference fields in declaration payloads use another declaration's local `Identifier`.
The compiler validates the generated schema's permitted asset types, resolves
nested and array references, and rejects missing references and cycles. It accepts
known configuration, asset and management types; activation declarations are
authored through `Activations`. Unknown future declaration types require a schema
update. The compiled result includes identifier mappings for status correlation.

Availability is checked by the existing engine against each enrollment's OS,
version, channel and observed capabilities. Publication is atomic on the server;
it does not make the device install all declarations atomically. Native/profile
setting conflicts retain Apple's ordinary conflict-resolution behavior.

The maintained live acceptance scenarios exercise this lifecycle on both device
and user channels of a macOS 26 VM. See
[Blueprint acceptance on macOS 26](../testing/blueprints-macos26.md) for the
reproduction command, native device evidence and platform limits.

## Configuration profiles

Configure `DM_PUBLIC_URL` as the public HTTPS base reached by devices. Upload
original `.mobileconfig` bytes:

```sh
dmctl configuration-profiles upload -file settings.mobileconfig
dmctl configuration-profiles list
dmctl configuration-profiles get PROFILE_REVISION
dmctl configuration-profiles download PROFILE_REVISION > retained.mobileconfig
```

Add the returned digest to a Blueprint declaration:

```json
{"Identifier": "legacy-settings", "ConfigurationProfile": {"Revision": "SHA256_FROM_UPLOAD"}}
```

Each upload is immutable and bounded to 4 MiB. Identical bytes share a revision.
Metadata retains `PayloadIdentifier` and `PayloadUUID` from the file. This upload
revision is a SHA-256 storage key, independent of declaration `ServerToken` values.
The server validates the envelope, known payloads and CMS signature integrity,
and rejects opaque encrypted profiles and the forbidden `com.apple.mdm` and
`com.apple.declarations` payload types. Unknown vendor preference payloads remain
available without claiming full schema validation. Original identifiers, UUIDs
and signatures are preserved. Consequently, identical profile identifiers can
still conflict across Blueprints; copying a declaration does not rewrite the file.

The default uses Apple's `LegacyProfile.ProfileURL`. It works on the platforms
and releases supported by that declaration, including OS 26 and OS 27. Explicit
`"UseProfileAssetReference": true` selects OS 27's `ProfileAssetReference` and creates a data asset
with the exact SHA-256, byte count, plist content type and MDM authentication.
`ConfigurationProfile`, `Revision`, and `UseProfileAssetReference` are convenience
fields. The compiler emits `LegacyProfile` and, when selected, `AssetData`
declarations using Apple’s fields.
Asset delivery accepts unsigned plist data; CMS-signed files use `ProfileURL`.
See [Apple's LegacyProfile reference](https://developer.apple.com/documentation/devicemanagement/legacyprofile).

Downloads require the pinned, valid MDM identity, enabled device and channel,
certificate-status checks, current assignment/compatibility and a declaration in
the last advertised snapshot. URLs include the full channel, enrollment ID and
parent ID. An older revision remains downloadable while that snapshot references
it and the declaration remains eligible. Removing its assignment or declaration
revokes access even before the next device sync. Profile contents are checked
against the target and profile scope at download; channels are not split
automatically. External `ProfileURL` values are also supported as ordinary native
payloads; the server does not fetch or validate external files.

In a split deployment publish through the DDM process. Set its `DM_PUBLIC_URL`
to the public MDM ingress URL. The MDM process authenticates device downloads and
forwards them through the existing signed, replay-protected private hop. The DDM
process checks membership and snapshots and returns authenticated profile bytes.

## HTTP and authorization

Routes are relative to `/admin/v1` and use the existing bearer authentication,
Cedar policies, audit trail and transaction handling:

| Method and route | Action |
|---|---|
| `POST /blueprints/validate` | `publishBlueprints` |
| `PUT /blueprints/{blueprint}` | `publishBlueprints` |
| `GET /blueprints`, `GET /blueprints/{blueprint}` | `readBlueprints` |
| `DELETE /blueprints/{blueprint}` | `publishBlueprints` |
| `PUT` or `DELETE /enrollments/{channel}/{id}/blueprints/{blueprint}` | `assignBlueprint` |
| `POST /configuration-profiles` | `manageConfigurationProfiles` |
| `GET /configuration-profiles`, `GET /configuration-profiles/{revision}` | `manageConfigurationProfiles` |
| `GET /configuration-profiles/{revision}/content` | `manageConfigurationProfiles` |

Use a Blueprint spec directly as the validation/publication JSON body, and raw
profile bytes for upload. GET/PUT return a quoted `ETag`; send it in `If-Match`
for updates and deletion. Creation omits `If-Match`. Lists use `limit` and `cursor`.
User assignments require the canonical enrollment ID and `?parent=DEVICE_ID`;
the CLI accepts `-channel user -parent DEVICE_ID`. Other channel names match the
existing enrollment API, including Shared iPad and User Enrollment.

Individual resources use `MDM::Blueprint` and `MDM::ConfigurationProfile` entities;
collections and validation use `MDM::System::"any"`. Assignment authorization
uses `MDM::Enrollment` with the Blueprint identifier in `context.blueprint`.
Audit records contain request metadata, not spec or profile bodies.

## Library use and persistence

Use `blueprint.NewDeclaration(identifier, typedPayload)` for generated Go payloads, or
provide a known type and raw payload. Call `blueprint.Compile(spec, options)` to
obtain declarations and mappings without I/O. `Options.Target` optionally checks
a target, and `Options.ConfigurationProfiles` resolves immutable hosted profile descriptors.
The caller supplies trustworthy descriptors; the compiler does not download URLs.
Pass `Compiled.Publication` to `Engine.PublishSet`, or use `PublishSetTx` inside
a transaction that also writes your own product metadata. Custom DDM stores need
the optional `PublicationLocker` capability on their transaction view.

The `server/blueprints` manager persists source, and `server/configurationprofile`
persists uploaded files. Both use the existing encrypted protocol-state store.
SQL joins Blueprint source and DDM writes in one unit of work; uploads also join
their admin audit transaction. The local `PublishSet` operation commits server
state; Apple’s device protocol does not define a publication resource. Publication locking uses 256
fixed database rows, so simultaneous first publications serialize without a
missing-row race. The initial `0001_init.sql` contains those rows for SQLite,
PostgreSQL and MySQL. Retained profiles participate in existing state key rotation
and backups; they have no automatic expiry or garbage collection in this version.

This feature supplies composition and deployment primitives. A graphical builder,
declaration catalog, smart groups, templates, approvals and synchronization with
Apple-hosted Blueprints remain product-layer concerns.

The [terminology audit](../research/blueprint-terminology-audit.md) records the
Apple references, package boundaries, and retained implementation names.
