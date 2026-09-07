# 0047: Enrollment authentication and optional security services

Status: accepted for implementation (2026-09-06).

## Context and evidence

The library implements protocols; ownership rules belong to its caller. ADE already
offers optional DEP enrichment and an admission-capable ProfileHook. Strict signed
MachineInfo verification is the default; audit mode deliberately permits unverified
input. Neither missing DEP wiring nor explicitly selecting audit mode is a missing
mandatory Apple protocol check.

Our account-driven implementation incorrectly consumed the access bearer at profile
retrieval and substituted a proprietary query credential. Apple's onboarding and
simple authentication documentation require reuse of the access bearer on subsequent
requests. macOS omits it on the device channel, including device enrollment, and uses
it on the user channel. iOS, iPadOS and visionOS use it on all channels. Apple's
published OAuth2 enrollment flow does not specify PKCE parameters; we will not require
them. Our upstream OIDC browser flow retains S256.

Reference checkouts inspected, independently of this implementation:

| Project | Commit | Relevant implementation |
|---|---|---|
| Zentral | c7947511a1f7323d61866e4cfe9591e88119ea7d | Persistent UserEnrollmentSession, certificate subject association, conditional enrollment-secret checks, nginx CRL verification |
| Fleet | 4877c4f02d46c2775b93a91f4d45e1d5afe590a4 | Optional ABM-only admission, IdP bearer association, GCRA quotas with atomic Redis state and server time |
| MicroMDM | 904493b9500ffc8a21846846781e362f5c612107 | Certificate depot and pin admission; certificate pinning alone is not revocation |

## Decision

Carry a redacted bearer in mdm.Request. Retain access tokens until expiry or explicit
invalidation; atomically exchange client/redirect/scope-bound codes and refresh tokens.
Associate a random enrollment reference and issued certificate with the authenticated
subject, Managed Apple Account, origin and platform. Bind the device-generated channel
identity without comparing an email address to an EnrollmentID or UDID. Certificate
proof and association are required independently of bearer proof. Return a typed
reauthentication challenge only for a recognized account-driven enrollment. Preserve
traditional enrollment error semantics and Apple's macOS device-channel exception.

Add opt-in issuer-scoped certificate registration, irreversible revocation, signed CRL
and OCSP publication, and RFC 8555 revokeCert. Persist issuance before returning a
certificate. Validate status before service side effects, including SCEP renewal, for
every certificate transport. Checkout and reenrollment do not decide revocation policy.
CRL lifetimes, refresh windows and publication URLs are explicit deployment settings.

Add opt-in GCRA limits with atomic multi-bucket accounting, bounded state, expiry,
database time, typed unavailable errors and Retry-After. The default peer key uses the
socket address; forwarded addresses require explicit trusted proxy configuration.

A small transactional byte-record store supports memory and SQLite/PostgreSQL/MySQL.
Components retain their own typed interfaces and records. SQL transactions lock fixed
shards in deterministic order before reading database time and updating state. Fixed
lock rows avoid an unbounded lock table. Protocol libraries never import SQL drivers
or the reference server. Token records store hashes, never bearer values.

## Validation and migration

Test Apple-shaped requests, cross-identity replay, exchange races, channel exceptions,
reauthentication retries, revocation across transports, independently parsed CRL/OCSP,
ACME authorization and shared quotas. Run module, race, layout, storage, schema and
integration checks; report unavailable infrastructure and physical-device validation
explicitly. Existing query-token profiles require a deliberate re-enrollment migration;
they are not silently accepted as bearer-authenticated enrollments. Import certificate
DER into the registry before enabling fail-closed status checks on an existing fleet.

## References

- [Apple onboarding](https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment)
- [Apple simple authentication](https://developer.apple.com/documentation/devicemanagement/implementing-the-simple-authentication-account-driven-enrollment-flow)
- [Apple OAuth2 authentication](https://developer.apple.com/documentation/devicemanagement/implementing-the-oauth2-authentication-account-driven-enrollment-flow)
- [Apple WWDC 2021 session 10136](https://developer.apple.com/videos/play/wwdc2021/10136/)
- [RFC 6749 §4.1.3](https://www.rfc-editor.org/rfc/rfc6749#section-4.1.3)
- [RFC 8555 §7.6](https://www.rfc-editor.org/rfc/rfc8555#section-7.6)
- [RFC 5280](https://www.rfc-editor.org/rfc/rfc5280), [RFC 6960](https://www.rfc-editor.org/rfc/rfc6960)
- Records 0008, 0027, 0028, 0031; docs/security/threat-model.md.
