# 0051: Content-cache metrics as an embeddable library

## Context

Apple's OS 27 seed adds an OpenAPI contract for reports produced by Content Cache servers. These reports go to a deployment-selected collector. They are separate from MDM check-ins and DDM status reports.

## Decision

`devicemanagement/contentcache` provides the report model, decoding, validation and an HTTP receiver. Its source contract is `openapi/content-cache/metrics_report.json` at Apple commit `b0180185a5e4077070710033341b71d0cbe1a18a`, retained with its license in package testdata. The package is opt-in; its reviewed fixture is verified against the adopted OS 27 source.

Consumers mount the receiver, configure TLS and provide authorization and acceptance callbacks. Authorization runs before the body is read. Acceptance returns success only once the consumer has stored or taken responsibility for the report. The package provides no queue, persistence, authentication scheme or `dmserver` route. An authenticated identity can be carried by middleware in the request context; report hostname and server GUID are not identity credentials.

The receiver accepts POST and PUT because the OpenAPI operation specifies POST `/metrics`, while the DDM declaration describes PUT to `ManagementStatusTarget`. The same report contract is applied to both. This decision accommodates an upstream discrepancy and does not establish physical-device interoperability.

## Validation and responses

All 88 report properties and the parent/peer records are modeled. Pointers preserve omitted versus explicit zero, false and empty scalar values. Decoding rejects duplicate properties, invalid UTF-8, wrong types and null known properties. Required fields, RFC 3339 timestamps, UUIDs and declared enums are validated. Unknown properties are retained, as the OpenAPI does not prohibit them. No undocumented positive-counter limits or exact-version restriction are imposed.

The default report limit is 1 MiB and is configurable. Successful acceptance returns 202. Invalid reports return 400, authorization rejection 401, unsupported methods 405, oversized bodies 413, unsupported media types 415 and unavailable acceptance 503. Callback diagnostics are not returned to clients. Delivery guarantees, retry deduplication and retention belong to the consumer.

## Verification

Contract tests compare every modeled property against the retained Apple schema and round-trip complete and minimal reports. Receiver tests cover both methods, explicit zeros, unknown extensions, validation failures, authorization and acceptance errors, cancellation and body limits. Seed assessments also compare the assessed OpenAPI file with the reviewed fixture so upstream changes require another review.

## References

- [Library](../../../devicemanagement/contentcache/)
- [Apple OpenAPI](https://github.com/apple/device-management/blob/b0180185a5e4077070710033341b71d0cbe1a18a/openapi/content-cache/metrics_report.json)
- [Apple DDM content-cache settings](https://github.com/apple/device-management/blob/b0180185a5e4077070710033341b71d0cbe1a18a/declarative/declarations/configurations/content-cache.settings.yaml)
- [Issue #46](https://github.com/deploymenttheory/go-apple-dm/issues/46)
