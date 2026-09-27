# 0062: Reference-server package delivery

## Context

The applications library supplies verified installer revisions and storage adapters.
The reference server must expose those operations to authorized operators, host bytes
for enrolled devices and submit the appropriate native delivery protocol.

## Decision

`server/internal/app` configures the library and its filesystem, AWS, Azure and GCP
adapters. Package metadata, content uploads and source imports use separate admin
endpoints and permissions. Mutations require catalogue revision tokens. Uploads
stream raw request bytes, including HTTP/1.1 chunked bodies without Content-Length,
through the library's size, installer verification and storage readback checks.

`server/applicationpackages` validates the enrolled Mac's platform and management
capabilities against the pinned Apple schema. It prepares either an
`InstallEnterpriseApplication` command or a DDM package declaration and activation.
The application dispatches that plan through its existing command and declaration
services. SQL delivery participates in the shared local transaction, including
download-grant creation, protocol state and required audit capture.

Devices download through the server using an expiring bearer grant bound to one
enrollment and immutable content revision. Only the token hash is stored in the
grant record. Each request checks grant expiry, enrollment enablement and package
availability. Revocation removes the grant. The server generates the manifest with
its protected content URL; a catalogue-assigned manifest cannot override that URL.
Content supports HEAD, one byte range, ETags and interrupted-stream detection.

Download tokens are removed from the observed request path before server webhooks
and logs. Operator routes retain separate metadata, upload, import, download and
enrollment-delivery permissions. Cloud objects remain private; devices do not need
provider credentials or public bucket access.

Authorized package uploads, source imports and downloads have a 30-minute response
and storage-work deadline. Uploads also receive a 30-minute request-body deadline.
This avoids the ordinary server's 60-second transfer cutoff while keeping package
requests bounded. Unrelated endpoints retain their existing deadlines.

## Rationale

One verified content revision can support both delivery protocols without duplicating
verification or exposing storage credentials. Separate metadata and content operations
allow review before upload, independent permissions and retry of failed content
replacement. A grant per delivery supports revocation without altering package bytes.
Native eligibility checks reject unsupported platform or installer preferences before
grant creation and dispatch.

## Constraints

Delivery acceptance means queued protocol work, not installation. Validate command
results or DDM status and then observe the installed application on the device.
Native DDM package delivery requires a supported macOS version; use MDM on macOS 15.

Grants default to one hour and expire within seven days. Keep a grant alive throughout
installation or prepare a new delivery. Revocation stops future downloads; it does
not uninstall the app or remove a DDM assignment. External proxies must redact bearer
URLs and allow the configured transfer duration.

Blob storage does not share a SQL transaction with the catalogue. Upload and deletion
use the library's staging, revision and cleanup contracts. In-memory dispatch cannot
provide SQL rollback across the grant, command and declaration stores; failed dispatch
attempts to revoke its grant. Live cloud account permissions and native device
installation require separate deployment acceptance.

## Verification

`server/applicationpackages` tests exercise eligibility, grant expiry and revocation,
immutable downloads and MDM/DDM payload preparation. The application tests exercise
authenticated API operations on memory and SQLite stores, actual chunked HTTP uploads,
oversized and interrupted bodies, range downloads and token redaction. SQL fault
injection verifies that failed command, declaration, assignment or audit writes retain
no delivery state. Transfer tests renew expired connection deadlines through auditing
and verify bounded storage contexts.

`server/internal/dmctl` tests cover the package verbs, request framing, revision
preconditions, paging and explicit APNs wake results. Run `make test`,
`make test-storage`, `make test-e2e` and `make coverage` for the unchanged overall and
per-package coverage requirement. Run `make verify-server-module-installation` to
check the server against its published library dependency with workspace mode disabled.

## References

- [Application package operations](../../operations/application-packages.md)
- [Verified package library design](0061-verified-application-packages.md)
- [Administrative API and authorization](0034-admin-api-and-authorization.md)
- [Native package schema](../../../third_party/apple-device-management/current/declarative/declarations/configurations/package.yaml)
