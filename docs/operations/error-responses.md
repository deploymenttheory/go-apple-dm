# Error responses and published codes

A failure is read by three audiences, and each receives what it can act on.

| Audience | Who acts | What they receive |
|---|---|---|
| Operator | the person running the server | one log record per failure, with the request id, the error group and the operator-facing detail; never a response body |
| Client | an administration API caller, whether a person's tool or another service | an [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457.html) problem document with a stable `type`, and `Retry-After` when a retry may succeed |
| Device | an Apple device on a protocol route | the HTTP status alone, or one of Apple's error documents where Apple defines one; never this server's prose |

## Problem documents

The administration API answers a failure with `application/problem+json`:

```json
{
  "type":     "urn:deploymenttheory:apple-dm:error:ddm-not-found",
  "title":    "the declaration, declaration set or enrollment does not exist",
  "status":   404,
  "detail":   "resolve declaration \"com.acme.settings\": the declaration, declaration set or enrollment does not exist",
  "instance": "urn:deploymenttheory:apple-dm:request:K7QX2M4V9A1B"
}
```

### What to depend on

`type` is the contract. It identifies the condition, it is stable across releases, and it
is what a client branches on. A code is never reused for a different condition and never
renamed; a condition that splits gains new codes and retires the old one. Each `type` is
the code lower-cased, without the `DM-` prefix, under
`urn:deploymenttheory:apple-dm:error:`, so `DM-PKI-CSR-INVALID` is
`urn:deploymenttheory:apple-dm:error:pki-csr-invalid`.

`status` carries the classification, so a client that only needs retry-or-not can use it
alone. `Retry-After` accompanies a 429 or a 503, either the delay the failure knows or a
default of a few seconds.

`instance` carries the request id, the same value as the `X-Request-Id` response header
and the `request.id` field of the operator's log record. Quote it when reporting a
failure to an operator.

`title` and `detail` are written for a person. They are clarified and reworded between
releases, so matching on them will break. `title` describes the condition and does not
vary between occurrences; `detail` describes the occurrence: the operation that was under
way and the prose the raising site wrote for the caller, or the condition's message when it
wrote none. It never carries the underlying cause. A storage driver's or the operating
system's text stays in the operator's log record, found by the request id.

### What is not published

A 5xx response publishes its status and nothing more: no `type`, no `detail`. The
deployment is at fault, the caller can neither diagnose nor act on the cause, and the
detail belongs in the server's log, where the request id finds it. An operator condition
whose own kind would be a 4xx, such as a secret the deployment does not define, is
answered 500 for the same reason.

A 4xx response always explains itself in `detail`, whether or not its condition is
catalogued. An uncatalogued rejection carries `"type": "about:blank"`, the value
RFC 9457 reserves for a problem with no semantics beyond its status. Treat an
unrecognised `type` as you would `about:blank`.

### Reading a problem document in Go

`server/problem.Parse` turns a failed response back into an error that classifies:
`errors.Is` against the catalogue in `devicemanagement/fault`, `fault.CodeOf` and
`fault.KindOf` work on the client as they do on the server, and the request id is
attached as an attribute.

## Kinds and statuses

Every condition has a kind, named after the canonical error codes gRPC and Google's API
design guide share, and every kind aligns with one status:

| Kind | Status | Meaning |
|---|---|---|
| `invalid_argument` | 400 | the request was understood and rejected |
| `unauthenticated` | 401 | a missing or unusable credential |
| `permission_denied` | 403 | an authenticated caller without the permission, or a device the server refuses |
| `not_found` | 404 | an addressed thing that does not exist |
| `conflict` | 409 | the request disagrees with existing state |
| `gone` | 410 | a thing that existed and was deliberately removed |
| `payload_too_large` | 413 | a body over the bound the route accepts |
| `unsupported_media_type` | 415 | a body in a format the route does not read |
| `resource_exhausted` | 429 | a rate limit, quota or capacity bound |
| `internal` | 500 | a failure the caller cannot act on |
| `unimplemented` | 501 | an operation this server does not provide |
| `upstream` | 502 | a dependency answered, but not usefully |
| `unavailable` | 503 | a dependency is configured but not usable now |
| `deadline_exceeded` | 504 | an operation ran out of time |

## Catalogue

The tables below are generated from the catalogue and checked by
`internal/layout`; `UPDATE_DOCS=1 go test ./internal/layout -run TestErrorResponsesTableMatchesCatalogue`
regenerates them.

<!-- catalogue:start -->
### Conditions published to API callers

| Code | Kind | Status | Title |
|---|---|---|---|
| `DM-ACME-NOT-FOUND` | `not_found` | 404 | the ACME record does not exist |
| `DM-ACTIVATIONLOCK-BYPASS-CODE-INVALID` | `invalid_argument` | 400 | the server bypass code is not valid |
| `DM-ADE-CONFLICT` | `conflict` | 409 | the request conflicts with the stored Automated Device Enrollment state |
| `DM-ADE-CONSUMER-KEY-MISMATCH` | `conflict` | 409 | the consumer key differs from the stored token and replacement was not forced |
| `DM-ADE-INVALID` | `invalid_argument` | 400 | the Automated Device Enrollment request is not acceptable |
| `DM-ADE-NOT-FOUND` | `not_found` | 404 | the Automated Device Enrollment record does not exist |
| `DM-ADE-PROFILE-INVALID` | `invalid_argument` | 400 | the Automated Device Enrollment profile is not acceptable |
| `DM-APNS-REQUEST-INVALID` | `invalid_argument` | 400 | the app notification request is not acceptable |
| `DM-APPARTIFACT-INVALID` | `invalid_argument` | 400 | the application artifact is not valid |
| `DM-APPARTIFACT-TOO-LARGE` | `payload_too_large` | 413 | the application artifact exceeds the inspection limit |
| `DM-APPARTIFACT-UNSUPPORTED` | `invalid_argument` | 400 | the application artifact type is not supported |
| `DM-APPIDENTITY-INVALID` | `invalid_argument` | 400 | the bundle or Mach-O executable is not valid |
| `DM-APPIDENTITY-TOO-LARGE` | `payload_too_large` | 413 | the application metadata exceeds the size limit |
| `DM-APPSBOOKS-INVALID` | `invalid_argument` | 400 | the Apps and Books request is not acceptable |
| `DM-APPSBOOKS-LIMIT-EXCEEDED` | `resource_exhausted` | 429 | the request exceeds the current Apps and Books service limits |
| `DM-APPSTORE-LISTING-NOT-FOUND` | `not_found` | 404 | the App Store listing does not exist |
| `DM-APPSTORE-QUERY-INVALID` | `invalid_argument` | 400 | the App Store query is not valid |
| `DM-AXM-ACTIVITY-INVALID` | `invalid_argument` | 400 | the activity violates the Apple School and Business Manager API rules |
| `DM-AXM-INVALID` | `invalid_argument` | 400 | the Apple School and Business Manager API request is not acceptable |
| `DM-AXM-LIMIT-INVALID` | `invalid_argument` | 400 | the page limit is out of range |
| `DM-DDM-CONFLICT` | `conflict` | 409 | the request conflicts with the stored declaration state |
| `DM-DDM-DECLARATION-INVALID` | `invalid_argument` | 400 | the declaration failed validation |
| `DM-DDM-INVALID` | `invalid_argument` | 400 | the declarative management request is not acceptable |
| `DM-DDM-NOT-FOUND` | `not_found` | 404 | the declaration, declaration set or enrollment does not exist |
| `DM-DDM-UNKNOWN-TYPE` | `invalid_argument` | 400 | the declaration type is not one this server serves |
| `DM-ENROLLMENT-CONFLICT` | `conflict` | 409 | the request conflicts with the stored enrollment |
| `DM-ENROLLMENT-DISABLED` | `conflict` | 409 | the enrollment is disabled |
| `DM-ENROLLMENT-INVALID` | `invalid_argument` | 400 | the enrollment request is not acceptable |
| `DM-ENROLLMENT-NOT-FOUND` | `not_found` | 404 | the enrollment does not exist |
| `DM-ENROLLMENT-TOKEN-EXPIRED` | `gone` | 410 | the enrollment token has expired |
| `DM-ENROLLMENT-TOKEN-NOT-FOUND` | `not_found` | 404 | the enrollment token does not exist |
| `DM-ENROLLMENT-TOKEN-USED` | `conflict` | 409 | the enrollment token has already been used |
| `DM-ENROLLMENT-USER-CHANNEL-REQUIRED` | `invalid_argument` | 400 | the operation requires a user channel |
| `DM-INVENTORY-CONFLICT` | `conflict` | 409 | the identity or revision conflicts with the stored record |
| `DM-INVENTORY-INVALID` | `invalid_argument` | 400 | the inventory request is not acceptable |
| `DM-INVENTORY-JOB-STOPPED` | `conflict` | 409 | the inventory job has stopped |
| `DM-INVENTORY-NOT-FOUND` | `not_found` | 404 | the inventory record does not exist |
| `DM-MANIFEST-INVALID` | `invalid_argument` | 400 | the manifest input is not acceptable |
| `DM-MDM-COMMAND-INVALID` | `invalid_argument` | 400 | the command is not acceptable |
| `DM-OSVERSION-MALFORMED` | `invalid_argument` | 400 | the operating system version is malformed |
| `DM-PKI-CERTIFICATE-EXPIRED` | `permission_denied` | 403 | the certificate is outside its validity period |
| `DM-PKI-CERTIFICATE-NOT-FOUND` | `not_found` | 404 | the certificate does not exist |
| `DM-PKI-CERTIFICATE-REVOKED` | `conflict` | 409 | the certificate has already been revoked |
| `DM-PKI-CONFLICT` | `conflict` | 409 | the transition conflicts with the identity's current state |
| `DM-PKI-CSR-INVALID` | `invalid_argument` | 400 | the certificate signing request is not valid |
| `DM-PKI-INVALID` | `invalid_argument` | 400 | the certificate request is not acceptable |
| `DM-PKI-ISSUER-UNKNOWN` | `not_found` | 404 | the issuer is not known to this server |
| `DM-PKI-POLICY-VIOLATION` | `permission_denied` | 403 | the certificate request violates the issuing policy |
| `DM-PKI-REVOCATION-INVALID` | `invalid_argument` | 400 | the certificate, reason or issuer is not acceptable |
| `DM-PLIST-TOO-DEEP` | `invalid_argument` | 400 | the plist nesting exceeds the depth limit |
| `DM-PLIST-TOO-LARGE` | `payload_too_large` | 413 | the plist exceeds the size limit |
| `DM-PLIST-UNKNOWN-FORMAT` | `invalid_argument` | 400 | the input is neither an XML nor a binary plist |
| `DM-PREDICATE-MALFORMED` | `invalid_argument` | 400 | the predicate could not be parsed |
| `DM-PREDICATE-TYPE-MISMATCH` | `invalid_argument` | 400 | the predicate compares values of different types |
| `DM-PREDICATE-UNSUPPORTED` | `invalid_argument` | 400 | the predicate uses a construct this server does not support |
| `DM-PROFILE-INVALID` | `invalid_argument` | 400 | the configuration profile is not acceptable |
| `DM-PROFILE-MALFORMED` | `invalid_argument` | 400 | the configuration profile could not be parsed |
| `DM-PUSHCERT-INVALID` | `invalid_argument` | 400 | the push certificate or its key is not valid |
| `DM-PUSHCERT-KEY-MISMATCH` | `invalid_argument` | 400 | the private key does not match the push certificate |
| `DM-PUSHCERT-TOPIC-MISSING` | `invalid_argument` | 400 | the push certificate carries no APNs topic |
| `DM-RECORD-NOT-FOUND` | `not_found` | 404 | the record does not exist |
| `DM-SCHEMA-INVALID` | `invalid_argument` | 400 | the content failed schema validation |

### Conditions a device meets with an Apple error document

| Apple `code` | Kind | Status | Condition |
|---|---|---|---|
| `com.apple.psso.required` | `permission_denied` | 403 | the device must complete Platform SSO before enrolling |
| `com.apple.softwareupdate.required` | `permission_denied` | 403 | the device must update its operating system before enrolling |
| `com.apple.unrecognized.device` | `permission_denied` | 403 | the enrollment is not recognized by this server |
| `com.apple.well-known.failed` | `permission_denied` | 403 | service discovery failed for this device |

### Conditions a device meets as a bare status

| Kind | Status | Condition |
|---|---|---|
| `permission_denied` | 403 | account enrollment authorization is required |
| `permission_denied` | 403 | enrollment was denied |
| `permission_denied` | 403 | re-enrollment is not permitted for this enrollment |
| `permission_denied` | 403 | the CMS message has more than one signer |
| `permission_denied` | 403 | the CMS message has no signer |
| `invalid_argument` | 400 | the CMS structure is malformed |
| `invalid_argument` | 400 | the CSR was rejected |
| `invalid_argument` | 400 | the JWS algorithm is not supported |
| `invalid_argument` | 400 | the JWS is malformed |
| `invalid_argument` | 400 | the JWS key is unsupported or malformed |
| `invalid_argument` | 400 | the JWS protected header is not valid |
| `permission_denied` | 403 | the JWS signature does not verify |
| `invalid_argument` | 400 | the MachineInfo breaks the presence rules |
| `invalid_argument` | 400 | the MachineInfo is malformed |
| `invalid_argument` | 400 | the MachineInfo is too large |
| `permission_denied` | 403 | the MachineInfo signature was not verified |
| `permission_denied` | 403 | the MachineInfo signer is not trusted |
| `invalid_argument` | 400 | the Mdm-Signature header is malformed |
| `invalid_argument` | 400 | the OAuth 2.0 grant is not valid |
| `invalid_argument` | 400 | the OAuth 2.0 request is not valid |
| `permission_denied` | 403 | the SCEP challenge was rejected |
| `invalid_argument` | 400 | the SCEP operation is not supported |
| `invalid_argument` | 400 | the Shared iPad user channel message has no UserShortName |
| `invalid_argument` | 400 | the algorithm is not supported |
| `permission_denied` | 403 | the attestation certificate chain does not verify |
| `invalid_argument` | 400 | the attestation certificate extension is malformed |
| `invalid_argument` | 400 | the attestation object is malformed |
| `permission_denied` | 403 | the attested key is not the requested key |
| `invalid_argument` | 400 | the authentication callback is not acceptable |
| `permission_denied` | 403 | the authentication state has expired |
| `permission_denied` | 403 | the authentication state is not known |
| `permission_denied` | 403 | the browser binding does not match |
| `permission_denied` | 403 | the certificate chain could not be verified |
| `invalid_argument` | 400 | the challenge is not valid |
| `invalid_argument` | 400 | the check-in MessageType is not known |
| `invalid_argument` | 400 | the command response is not valid |
| `invalid_argument` | 400 | the content cache report is not valid |
| `invalid_argument` | 400 | the declarative management endpoint is malformed |
| `permission_denied` | 403 | the device identity certificate is missing |
| `permission_denied` | 403 | the device identity does not match the enrollment |
| `permission_denied` | 403 | the enrollment association does not match |
| `invalid_argument` | 400 | the enrollment identity is not valid |
| `permission_denied` | 403 | the enrollment is disabled |
| `invalid_argument` | 400 | the enrollment mode does not match the discovery version |
| `unauthenticated` | 401 | the enrollment must re-authenticate |
| `permission_denied` | 403 | the enrollment was rejected |
| `permission_denied` | 403 | the freshness code does not match |
| `permission_denied` | 403 | the id_token was not accepted |
| `permission_denied` | 403 | the identity has no Managed Apple Account |
| `permission_denied` | 403 | the identity provider denied access |
| `unauthenticated` | 401 | the ingestion credential is not valid |
| `invalid_argument` | 400 | the message carries neither UDID nor EnrollmentID |
| `invalid_argument` | 400 | the message is not acceptable |
| `unimplemented` | 501 | the message type is not served |
| `invalid_argument` | 400 | the request carries no MachineInfo |
| `permission_denied` | 403 | the signature could not be verified |
| `permission_denied` | 403 | the signing time is outside the certificate's validity |
| `permission_denied` | 403 | the statement carries no attestation |
| `payload_too_large` | 413 | the status report exceeds the accepted size |
| `invalid_argument` | 400 | the status report is malformed |
| `permission_denied` | 403 | the user channel is not authenticated |
| `gone` | 410 | the user channel is not managed |
<!-- catalogue:end -->

## What a device receives

A device never reads a problem document. On the check-in and command channels it acts on
the status and on Apple's own error documents, and the transport applies Apple's rules
over the kind:

- An account-driven enrollment whose access token must be renewed is the only failure
  answered 401, and it carries the `WWW-Authenticate` challenge that tells the device
  how. Any other condition of kind `unauthenticated` is answered 400, because a device
  answered a bare 401 may unenroll.
- An enrollment this server does not know is answered 403 with Apple's
  `com.apple.unrecognized.device` document when the deployment chose to have such devices
  unenroll, and 400 otherwise, so a device that is merely unknown here is not told to
  discard its management.
- A `UserAuthenticate` for a user this server does not manage is answered 410, which
  Apple's protocol defines as the signal to stop managing that user channel.
- Apple's error documents carry `code`, `description` and `message` as Apple defines
  them: `description` is for a log, `message` may be shown to the user. Nothing in them is
  this server's vocabulary.

`ErrorChain`, with `ErrorDomain`, `ErrorCode`, `LocalizedDescription` and
`USEnglishDescription`, is the structure a device sends in a command response with
`Status` `Error`. It travels the other way, from device to server, and is stored verbatim;
it is never a server-to-device error format. Declarative status reasons, with `Code`
values such as `Error.ConfigurationCannotBeApplied`, are likewise the device's report and
are counted and stored as received.

## Related failures with their own formats

| Surface | Format |
|---|---|
| ACME | RFC 8555 problem documents, `urn:ietf:params:acme:error:…` |
| SCEP | the failure responses that protocol defines |
| Health probes | a JSON report per check |

## References

- [Decision record 0061](../research/decisions/0061-error-classification-and-codes.md) for
  the classification, audiences and why sentinels stay with their producer
- [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457.html), which obsoletes
  [RFC 7807](https://www.rfc-editor.org/rfc/rfc7807.html)
- [Apple, `ErrorCodeUnrecognizedDevice`](https://developer.apple.com/documentation/devicemanagement/errorcodeunrecognizeddevice)
  and the other error documents under `mdm/errors` in Apple's device management schema
- [Reference server APIs](reference-lab.md) for the routes themselves
