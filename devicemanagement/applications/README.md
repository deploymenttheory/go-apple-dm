# Application packages

Use `applications.Manager` for metadata, verified uploads, immutable content revisions,
installation manifests and change history. Supply a `state.Store` and one or more
`BlobStore` adapters. Keep the store and its package objects private.

## Configure storage

| Package | Official SDK | Configuration supplied by the caller |
| --- | --- | --- |
| `applications/aws` | AWS SDK for Go v2, `service/s3` | `*s3.Client`, existing bucket, optional object prefix |
| `applications/azure` | Azure SDK for Go, `storage/azblob` | `*azblob.Client`, existing container, optional object prefix |
| `applications/gcp` | Google Cloud SDK for Go, `cloud.google.com/go/storage` | `*storage.Client`, existing bucket, optional object prefix |
| `applications/filesystem` | Go `os.Root` | Existing private directory; close the store after use |

Configure SDK credentials through the provider's normal credential mechanisms. Apply
private bucket/container access policies and restrict the SDK identity to the package
prefix. The adapters do not create buckets, change access policies or add public ACLs.
Supply retries, endpoints and deadlines through the SDK client. Close the GCP client
when its owner stops. Cloud retention/versioning may retain historical object versions
after deletion; configure that policy separately.

S3 uses single-object uploads through 16 MiB and bounded multipart uploads above that
size, up to 10,000 parts of 16 MiB. Azure streams 4 MiB blocks with two concurrent
buffers. GCP uses resumable uploads with a 4 MiB chunk buffer. Filesystem storage
stages a mode-0600 file and atomically links it into place. All adapters reject
replacement of an existing object key. Manager-generated keys identify immutable
content revisions, independent of the filename supplied by an operator.

## Create and upload

1. Create a `Metadata` value with `packageName` and `fileName`. Set `bundleID` and
   `version` before native delivery. Use pointers for optional values; explicit
   `false`, zero and empty strings remain distinct from omitted values.
2. Call `Manager.Create`. Retain the returned ID and revision token.
3. Choose a configured backend. Upload a stream with `Manager.Upload`, import a
   confined local path with `filesystem.Store.Import`, or import an allowlisted HTTPS
   source with `HTTPSSource.Import`.
4. Supply independently obtained digest expectations when available. The metadata
   digest fields and the upload's optional SHA-256 argument must all match.
5. Use the returned immutable content revision for manifests and downloads.
6. Pass the current catalogue revision to every update, upload, manifest mutation,
   note or deletion. Refresh the record after a conflict; do not overwrite blindly.

Uploads stage bounded source bytes in a private temporary file, validate the installer
archive and signer, calculate digests, write the backend object and read it back.
Only matching stored bytes become associated with the catalogue entry. A failed
replacement leaves the previous verified revision available. Metadata updates preserve
existing content snapshots. A new content revision clears an assigned manifest so an
old manifest cannot accidentally describe new bytes.

The default input limit is 2 GiB. Set `Config.MaxBytes` and private scratch capacity
for the expected package sizes and concurrency. Use request deadlines for streaming
sources. HTTPS imports require explicit allowed authorities and enforce the same
restriction after redirects. Configure transport-level network restrictions when the
source hostname or DNS is untrusted. Stored HTTPS provenance excludes signed queries.

## Verify packages

Require Apple's trusted installer signing chain by default. For an isolated lab,
configure both private trust anchors and `AllowPrivateSigner`; an arbitrary signature
alone is insufficient. Optionally constrain Team ID and require a verified timestamp.

Inspect `Content.Verification` for archive checksum, signature, trust, signer,
Team ID and timestamp results. Revocation and notarization remain `not-checked`;
these checks do not establish native Gatekeeper acceptance. Do not report them as passes.

Use SHA-256 for canonical content identity. MD5 exists for Apple manifest compatibility.
Treat `sha3512` as **SHA3-512** and `sha512` as **SHA-512**. Never substitute one for the
other. Provider ETags and transfer metadata do not replace readback verification.

## Metadata field mapping

The table accounts for every property in the Jamf Pro 11.31.1 `Package` schema.
The package model separates editable metadata, measured content and assigned manifests
instead of mixing assertions with verification results. `Record.Resource` and the
JSON/CSV exporters provide the flattened package view.

| Jamf property | Library field / behavior |
| --- | --- |
| `id` | `Record.ID`; generated, read-only |
| `packageName` | `Metadata.PackageName`; package and manifest display name |
| `fileName` | `Metadata.FileName`; validated `.pkg` basename, never a storage path |
| `categoryId` | `Metadata.CategoryID`; optional caller-owned category reference |
| `info` | `Metadata.Info`; descriptive information, up to 16 KiB |
| `notes` | `Metadata.Notes`; editable notes, separate from append-only history |
| `priority` | `Metadata.Priority`; nonnegative submission priority; `OrderForDelivery` defaults omission to 10 and sorts lower values first |
| `osRequirements` | `Metadata.OSRequirements`; comma-separated exact versions or trailing `.x` patterns, checked by `SupportsOS` and `CheckNativeDelivery` |
| `fillUserTemplate` | `Metadata.FillUserTemplate`; retained; native delivery rejects `true` |
| `indexed` | `Record.Indexed`; `null` while no package payload index is available; never inferred from signature verification |
| `fillExistingUsers` | `Metadata.FillExistingUsers`; retained; native delivery rejects `true` |
| `swu` | `Metadata.SWU`; retained; native delivery rejects `true` |
| `rebootRequired` | `Metadata.RebootRequired`; retained; native delivery rejects `true` until an explicit restart workflow handles it |
| `selfHealNotify` | `Metadata.SelfHealNotify`; retained; native delivery rejects `true` |
| `selfHealingAction` | `Metadata.SelfHealingAction`; retained; native delivery accepts empty or `nothing`, rejects other actions |
| `osInstall` | `Metadata.OSInstall`; retained; native package delivery rejects `true`; use a separate OS installation workflow |
| `serialNumber` | `Metadata.SerialNumber`; license metadata; native delivery rejects a nonempty value because it cannot inject it into the installer |
| `parentPackageId` | `Metadata.ParentPackageID`; checked against an existing package; cycles and deletion of a referenced parent are rejected |
| `basePath` | `Metadata.BasePath`; retained; native delivery rejects a nonempty installer path override |
| `suppressUpdates` | `Metadata.SuppressUpdates`; retained; native delivery rejects `true` |
| `cloudTransferStatus` | `Record.CloudTransferStatus`; `MISSING`, `READY` or `DELETING`, describing the committed content; failed replacement does not invalidate an existing ready revision |
| `ignoreConflicts` | `Metadata.IgnoreConflicts`; retained; native delivery rejects `true` |
| `suppressFromDock` | `Metadata.SuppressFromDock`; retained; native delivery rejects `true` |
| `suppressEula` | `Metadata.SuppressEula`; retained; native delivery rejects `true` |
| `suppressRegistration` | `Metadata.SuppressRegistration`; retained; native delivery rejects `true` |
| `installLanguage` | `Metadata.InstallLanguage`; validated language tag; native delivery rejects a nonempty installer-language override |
| `md5` | Input expectation in `Metadata.Digests`; measured value in `Content.Digests` |
| `sha256` | Input expectation in `Metadata.Digests`; measured value in `Content.Digests` |
| `sha3512` | SHA3-512 input expectation and independently measured value |
| `hashType` | Explicit `MD5`, `SHA256`, `SHA512` or `SHA3-512`; computed content selects `SHA256` |
| `hashValue` | Digest for the explicit algorithm; conflicting expectations are rejected |
| `size` | `Content.Size`; exact measured bytes, exported as a number instead of a rounded display string |
| `osInstallerVersion` | `Metadata.OSInstallerVersion`; retained descriptive metadata, not an instruction to install an OS |
| `manifest` | `Record.Manifest.Data`; validated XML/binary Apple plist bound to a content revision; JSON encodes bytes as base64 |
| `manifestFileName` | `Record.Manifest.FileName`; validated `.plist` basename |
| `format` | `Metadata.Format` optionally constrains `flat-pkg`; `Content.Format` reports the verified archive format |

`bundleID`, `version`, explicit `sha512` and `expectedDigests` extend the flattened
export. Measured fields stay absent/null until content exists; expectations never
masquerade as observed hashes. `DecodeMetadata` rejects unknown and read-only fields.
`Update` replaces the full metadata value; omission clears an optional preference.
JSON null and omission both mean unspecified. Export JSON for lossless values; CSV
prefixes potential spreadsheet formulas with an apostrophe.

Installation preferences are not portable Jamf agent commands. Call
`CheckNativeDelivery` before either MDM or DDM submission and surface the complete
list of unsupported preferences. False or unset booleans request no extra action.
The library retains unsupported preferences so integrations can inspect them or
provide their own executor; native delivery must not silently ignore them.

Package priority orders server submission, not completion of asynchronous device
installs. Parent relationships do not imply installation dependencies. A stored
category ID is not a category-management service.

## Manage manifests and history

1. Generate a plist with `BuildManifest` using a URL that serves the exact content
   revision over HTTPS. Apply download authorization and an appropriate URL lifetime.
2. Use `AssignManifest` to validate and attach a supplied manifest. It must contain
   one package asset with matching identity, SHA-256 and optional MD5. Unsupported
   keys, chunk hashes, additional assets and non-HTTPS URLs are rejected.
3. Use `DeleteManifest` to clear an assignment while retaining verified package bytes.
4. Read paginated history with `History`. Append an operator note with
   `AddHistoryNote`. Export pages with `ExportHistoryJSON` or `ExportHistoryCSV`.
5. Delete children before a referenced parent. Retry a failed deletion with its
   existing revision token. A deletion tombstone blocks new uploads and reads while
   cleanup proceeds; committed history remains available after deletion.

Use the same verified content and manifest for native MDM and DDM package delivery.
Select the protocol according to device capabilities. The library does not install
reference-server routes, authorize operators, dispatch commands or assert that a
queued package has installed. Confirm installation through device observations.

## References

- [Apple installation manifests](https://developer.apple.com/documentation/devicemanagement/manifesturl)
- [Apple enterprise application command](https://developer.apple.com/documentation/devicemanagement/installenterpriseapplicationcommand)
- [Apple declarative package schema](https://github.com/apple/device-management/blob/release/declarative/declarations/configurations/package.yaml)
- [Jamf package schema](https://developer.jamf.com/jamf-pro/reference/get_v1-packages-id)
- [AWS S3 SDK](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/s3)
- [Azure Blob SDK](https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/storage/azblob)
- [Google Cloud Storage SDK](https://pkg.go.dev/cloud.google.com/go/storage)
