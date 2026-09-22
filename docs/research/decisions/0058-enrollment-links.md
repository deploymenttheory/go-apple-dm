# 0058: Single-use enrollment links

## Context

A device can already obtain a device-bound enrollment profile, but only through
`POST /admin/v1/enrollment-profiles` with an administrator credential, or through
Apple's ADE, account-driven and OTA flows. Manual enrollment of a known device, including
the local lab's virtual and physical Macs, needs a page the device can open in its own
browser without holding an administrator credential. Decision 0048 excludes fixture-only
routes from the shipped server, so the capability must be an operator feature with its own
contract.

## Decision

An administrator creates an enrollment link bound to one device identifier and the same
optional fields as a profile request (serial, product, OS version, Mac hardware, identity
method, access rights and scope). Creation requires `issueEnrollmentProfile`, the action
that already authorizes issuing that profile directly; listing and revocation use the same
action. The link carries 256 random bits as a URL path segment. The server stores only the
token's SHA-256 digest, which is also the link identifier, in the transactional protocol
state table under `enrollment-link:`.

- **Lifetime:** a link expires after its TTL, one hour by default and at most 24 hours.
  Metadata remains listable for 24 hours after expiry, then expires from the state table.
- **Single use:** `GET /enroll/links/{token}/profile` commits redemption in a state
  transaction before issuing the profile through `ExportEnrollmentProfile`. Admission
  policy, identity selection and issuance metadata therefore apply exactly as for the admin
  route. An issuance failure after redemption leaves the link redeemed.
- **Landing page:** `GET /enroll/links/{token}` renders a static HTML page with the trust
  profile and profile download links. It does not consume the link, so link previews and
  reloads are harmless.
- **Uniform refusal:** unknown, expired, revoked and redeemed tokens receive one identical
  404 response. Storage failures are logged and also answered with that response.
- **Evidence:** creation and revocation are administrative mutations and are audited as
  `admin-action`. Redemption publishes `enrollment-link-redeemed` with the link
  identifier, device and identity method.
- **CLI:** `dmctl enrollment-links create|list|revoke`.

## Rationale

Binding the link to one device identifier keeps the issued profile's identity, admission
and replacement metadata the same as those of an operator-issued profile. A link adds no
new issuance authority, only a deferred, single-use delivery of an already authorized
profile. That is why it reuses the existing action rather than introducing a new one.
Consuming before issuing keeps the single-use guarantee under concurrency: issuing first
could deliver two profiles for one link. The protocol state table already provides keyed
transactions, expiry and pruning on every storage backend, so no schema migration is
required.

A reusable or unbound link would amount to a public enrollment endpoint and would move
admission decisions to the link holder; account-driven enrollment already covers
user-authenticated self-service.

## Constraints

The URL is a bearer credential until it is redeemed or expires. Responses set
`Cache-Control: no-store`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`
and a content security policy that forbids framing and external resources. Operators should
deliver the URL over a channel suited to a short-lived credential. The landing page requires
the device to trust the server's HTTPS certificate before it loads. A private CA's trust
profile must therefore be installed beforehand or delivered by another channel.

## Verification

- `server/internal/app/enrollmentlinks_test.go`: lifecycle, the uniform refusal, expiry,
  revocation, paging, storage faults and consumption on issuance failure.
- `server/lab/enrollmentlink.go` (E2E-032): a simulator enrolls through the
  landing page and profile download, and reuse is refused.
- `server/internal/dmctl/enrollmentlinkverbs_test.go`: CLI requests and usage errors.

## References

- [Decision 0009: enrollment profiles](0009-enrollment-profiles.md)
- [Decision 0034: admin API and authorization](0034-admin-api-and-authorization.md)
- [Decision 0048: reference server acceptance lab](0048-reference-server-bench.md)
- [Reference server APIs](../../operations/reference-lab.md#enrollment-links)
- [Local lab acceptance testing](../local_lab_acceptance_testing.md)
