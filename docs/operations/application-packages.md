# Application packages

Create a catalogue record, upload or import its installer, then deliver a verified
content revision to an enrolled Mac. Use separate metadata and content operations.
Both native MDM and DDM delivery use the same verified package and storage adapters.

## Configure storage

Create `application-packages.json`:

```json
{
  "publicURL": "https://mdm.example.test",
  "scratchDir": "./package-scratch",
  "importDir": "./package-imports",
  "allowedHTTPSHosts": ["downloads.example.test"],
  "backends": {
    "local": {"kind": "filesystem", "directory": "./packages"},
    "aws": {"kind": "aws", "bucket": "mdm-packages", "region": "us-east-1", "prefix": "installers"},
    "azure": {"kind": "azure", "accountURL": "https://example.blob.core.windows.net", "container": "mdm-packages", "prefix": "installers"},
    "gcp": {"kind": "gcp", "bucket": "mdm-packages", "prefix": "installers"}
  }
}
```

Keep only the backends in use. Create the import directory before starting the
server. Resolve relative paths against the configuration file's directory.
Configure credentials through each official SDK's credential chain. Provision
cloud buckets/containers and their access policies separately; the server does not
create them or make uploaded objects public.

Start `dmserver` with `-application-packages-config application-packages.json`, or
set `DM_APPLICATION_PACKAGES_CONFIG`. Set `publicURL` to the device-reachable HTTPS
origin, including a port when needed. An omitted value uses the enrollment public
URL. Do not include a path, query or credentials.

Use `maxBytes` to change the default 2 GiB installer limit. Allocate scratch space
for staged uploads and signature verification. Use `teamID` to restrict installer
signers and `requireTimestamp` to require a signing timestamp. For an explicitly
private lab signer, set both `signerCAFile` and `allowPrivateSigner: true`. Configure
native device trust separately. Use `httpsCAFile` for a private HTTPS source CA;
this does not change the host or device trust store.

## Grant operator permissions

Grant each operation explicitly:

| Permission | Access |
| --- | --- |
| `readApplicationPackages` | Metadata, manifests, history and exports |
| `manageApplicationPackages` | Metadata changes, manifests, notes and deletion |
| `uploadApplicationPackage` | Installer upload to a configured backend |
| `importApplicationPackage` | Import from configured filesystem/HTTPS sources |
| `downloadApplicationPackage` | Verified installer download |
| `deliverApplicationPackage` | Delivery and download-grant revocation for an enrollment |
| `pushEnrollment` | APNs wake after an MDM delivery request |

Treat metadata as sensitive: it can contain license information. Scope delivery
permissions to the intended enrollments. Upload permission does not grant delivery.

## Create metadata and upload content

Create `package.json`:

```json
{
  "packageName": "DM Lab Status",
  "fileName": "dm-lab-status.pkg",
  "bundleID": "com.example.dmlabstatus",
  "version": "1.0",
  "priority": 10,
  "osRequirements": "15.x, 26.x, 27.x",
  "notes": "Guest test progress display"
}
```

Use the [complete metadata field reference](../../devicemanagement/applications/README.md)
for category, installer, digest, notification and preference fields. Retained
metadata does not imply a native installation mechanism can implement every
preference; delivery rejects unsupported requests.

```sh
dmctl application-packages create -file package.json -output json
dmctl application-packages upload PACKAGE_ID \
  -revision CATALOGUE_REVISION -backend local -file dm-lab-status.pkg \
  -timeout 30m -output json
```

Read `id` and `revision` from creation. Use the latest `revision` for each catalogue
mutation. Read `content.revision` from upload for delivery. Supply independently
obtained digest expectations in metadata or `-sha256` on upload when available.
Uploads accept HTTP/1.1 chunked transfer encoding without `Content-Length`.
The configured byte limit applies while reading. Interrupted streams do not commit
content. This is one streaming request; resumable, multi-request upload sessions
are not implemented.

Allow up to 30 minutes for an authorized upload, source import or package download.
These routes extend the server's ordinary 60-second response deadline; uploads also
extend the request-body deadline. The same 30-minute limit bounds storage work.
Configure any external proxy for that transfer duration and use the CLI's
`-timeout 30m` option. Other routes retain the ordinary server deadlines.

The upload verifies the signed archive, measures digests, writes immutable storage,
reads the stored bytes back, and attaches content only after verification succeeds.

Select `-backend aws`, `azure` or `gcp` to upload to a configured cloud backend.
Backend names are configuration keys, not fixed provider names.

Import an existing source through the same verification path:

```sh
dmctl application-packages import PACKAGE_ID \
  -revision CATALOGUE_REVISION -backend local -kind file -source dm-lab-status.pkg \
  -timeout 30m -output json

dmctl application-packages import PACKAGE_ID \
  -revision CATALOGUE_REVISION -backend aws -kind https \
  -source https://downloads.example.test/dm-lab-status.pkg -timeout 30m -output json
```

Use paths beneath `importDir`. Permit each HTTPS source host, including its port,
with `allowedHTTPSHosts`; redirects must also satisfy the allowlist. Imported
content is copied into the selected backend. Devices fetch from the reference
server's grant-protected URL, not the original source or a public cloud bucket.

## Deliver after enrollment

Wait for enrollment to complete and inventory to establish the device's OS and
management capabilities. Select an immutable content revision:

```sh
dmctl application-packages deliver PACKAGE_ID DEVICE_ID \
  -content-revision CONTENT_REVISION -method mdm -grant-ttl 24h -output json

dmctl application-packages deliver PACKAGE_ID DEVICE_ID \
  -content-revision CONTENT_REVISION -method ddm -grant-ttl 24h -output json
```

MDM queues `InstallEnterpriseApplication`; the CLI then sends a separately
authorized APNs wake. DDM publishes and assigns a package configuration and an
activation. DDM package delivery requires macOS 26 or later and the enrollment
capabilities specified by Apple's package schema. Use MDM on macOS 15.
See Apple's [MDM command](https://developer.apple.com/documentation/devicemanagement/installenterpriseapplicationcommand)
and [DDM package declaration](https://github.com/apple/device-management/blob/release/declarative/declarations/configurations/package.yaml).

Treat `Status: queued` as dispatch acceptance. Check command results or DDM status,
then verify the installed application and its launch on the device. A successful
upload or APNs wake is not installation evidence.

Download grants default to one hour and have a maximum lifetime of seven days.
Expiry, revocation, disabled enrollment or package deletion prevents further
fetches. Keep the grant valid throughout installation; prepare a fresh delivery
if installation must start after expiry. Do not log bearer download URLs at an
external reverse proxy. The reference server redacts its own observed URL paths.

```sh
dmctl application-packages revoke GRANT_ID DEVICE_ID
```

Revocation stops future downloads. It does not uninstall an application or remove
a DDM assignment. Remove the returned declaration-set assignment separately when
ending the DDM scenario.

## Maintain the catalogue

```sh
dmctl application-packages list -all -output json
dmctl application-packages get PACKAGE_ID -output json
dmctl application-packages update PACKAGE_ID -revision CATALOGUE_REVISION -file package.json
dmctl application-packages note PACKAGE_ID -revision CATALOGUE_REVISION -note 'Approved for this scenario'
dmctl application-packages history PACKAGE_ID -all -output json
dmctl application-packages download PACKAGE_ID -content-revision CONTENT_REVISION > reviewed.pkg
dmctl application-packages manifest-set PACKAGE_ID -revision CATALOGUE_REVISION -file manifest.plist
dmctl application-packages manifest-delete PACKAGE_ID -revision CATALOGUE_REVISION
dmctl application-packages export -format csv -fields id,packageName,fileName
dmctl application-packages history-export PACKAGE_ID -format json
dmctl application-packages delete PACKAGE_ID -revision CATALOGUE_REVISION
```

Updates replace editable metadata. Preserve fields intentionally. Content revisions
retain their original metadata snapshot. Upload new content to attach changed
installation identity or preferences to a new delivery revision. A new upload
clears the assigned manifest. Delivery generates a manifest for the selected
revision and its scoped download URL; an assigned external manifest is validated
catalogue metadata, not an override for the protected delivery URL.

Exports return one page and print the next cursor to stderr. Continue with
`-cursor NEXT_CURSOR`. For bulk deletion, supply `delete-multiple -file delete.json`:

```json
{"packages":[{"id":"PACKAGE_ID","revision":"CATALOGUE_REVISION"}]}
```

Check every returned result. Bulk deletion is not atomic across cloud objects.
Delete child records before their parent records. History remains available after
deleting a record.

## API and Go responsibilities

All operator endpoints use `/admin/v1`:

| Operation | Endpoint |
| --- | --- |
| Create/list metadata | `POST` / `GET /application-packages` |
| Read/update/delete metadata | `GET` / `PUT` / `DELETE /application-packages/{id}` |
| Upload installer stream | `POST /application-packages/{id}/upload?backend=NAME` |
| Import source | `POST /application-packages/{id}/source` |
| Assign/remove manifest | `POST` / `DELETE /application-packages/{id}/manifest` |
| Read history/add note | `GET` / `POST /application-packages/{id}/history` |
| Export metadata/history | `GET /application-packages/export`, `GET /application-packages/{id}/history/export` |
| Delete multiple records | `POST /application-packages/delete-multiple` |
| Download verified revision | `GET /application-packages/{id}/revisions/{revision}/content` |
| Prepare and queue delivery | `POST /enrollments/device/{id}/application-packages` |
| Revoke download grant | `DELETE /enrollments/device/{id}/application-packages/grants/{grant}` |

Send the current quoted catalogue revision in `If-Match` for record mutations.
Metadata requests use JSON. Uploads and manifests use raw bytes. Import JSON uses
`kind`, `location` and `backend`. Delivery JSON uses `packageId`, `contentRevision`,
`method` and optional `ttlSeconds`.

`devicemanagement/applications` owns metadata, source ingestion, verification,
content revisions, manifests and history. Its provider packages implement storage
with the official SDKs. `server/applicationpackages` prepares native delivery and
manages enrollment-scoped download grants. `server/internal/app` configures those
components and implements the authenticated API, streaming downloads and command
or declaration dispatch. `server/internal/dmctl` is the API client.
