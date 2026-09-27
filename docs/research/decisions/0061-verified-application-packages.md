# 0061: Verified application packages and storage adapters

## Context

MDM and declarative package delivery need the same catalogue of installer metadata,
verified package bytes and installation manifests. Callers must be able to register
metadata before uploading content and choose filesystem or cloud storage without
reimplementing package verification in each server.

## Decision

`devicemanagement/applications` owns metadata, immutable content revisions, manifests
and history. A caller supplies a `state.Store`, configured `BlobStore` adapters and
verification policy. The `filesystem`, `aws`, `azure` and `gcp` subpackages implement
storage; cloud adapters use the providers' official Go SDKs.

Upload stages a bounded stream privately, verifies the installer archive and signing
chain, checks expected digests, writes an immutable object and reads it back. Only a
matching readback can be committed to the catalogue. Catalogue changes use revision
tokens and transactional state updates. A failed replacement preserves the previous
content revision. Failed uploads attempt to delete uncommitted objects and report
cleanup errors. Deletion retains a tombstone until object cleanup succeeds.

The metadata model accounts for every field in the recorded Jamf package schema.
Editable expectations remain separate from measured content and assigned manifests.
Content snapshots preserve the metadata used for their upload. Native eligibility
checks reject preferences that require an unavailable agent action. Retaining a
preference does not claim that Apple's native installer implements it.

The library supplies manifests and content to both delivery protocols. Authentication,
HTTP upload framing, download authorization, device capability selection, command or
declaration submission and installation acceptance belong to the consuming server.

## Rationale

One verification path gives local, HTTPS and cloud-backed uploads the same acceptance
requirements. Immutable objects and revision checks prevent a concurrent metadata edit
from silently attaching content verified against a stale record. Readback verifies
stored bytes independently of provider ETags. Keeping cloud configuration in the SDK
clients allows callers to choose credentials, endpoints, retries and deadlines.

## Constraints

State and object storage do not share a transaction. Backend failures can prevent
cleanup; callers must handle the returned error. Cloud retention policies can retain
historical object versions after deletion.

Package verification checks archive integrity and installer trust. Revocation and
notarization remain unchecked, and verification does not establish native Gatekeeper
acceptance. Private signing requires explicit trust anchors and an explicit policy
option. HTTPS sources require an authority allowlist; callers supply transport-level
network restrictions for untrusted DNS or destinations.

## Verification

Run `go test -race ./devicemanagement/applications/...` for metadata round trips,
schema field coverage, parent cycles, immutable revisions, digest and signature
failures, readback corruption, concurrent updates, manifests and cleanup failures.
Provider tests exercise the real SDKs against HTTP fixtures, including multipart S3
uploads and ranged reads. These tests do not establish live cloud account acceptance.
Filesystem tests cover confined paths, atomic publication and injected I/O failures.

Run `make test`, `make test-storage`, `make test-e2e` and `make coverage` for the
repository's combined overall and per-package coverage gate. Run `make docs-check`
and `make lint` for documentation and module boundaries.

## References

- [Application package library guide](../../../devicemanagement/applications/README.md)
- [Repository layering](0044-repository-layout.md)
- [Recorded package schema fields](../../../devicemanagement/applications/testdata/jamf-package-fields.json)
