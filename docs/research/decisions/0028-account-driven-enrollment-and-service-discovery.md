# 0028: Account-driven enrollment and service discovery

Status: accepted
Date: 2026-09-02
Phase: 6

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment>
- Doc: <https://developer.apple.com/documentation/devicemanagement/get-.well-known-com.apple.remotemanagement> (`model-family`, `user-identifier`, `WellKnown{Servers[{Version, BaseURL}]}`)
- Doc: <https://developer.apple.com/documentation/devicemanagement/implementing-the-simple-authentication-account-driven-enrollment-flow> (steps 1 to 12: redirect without query parameters, Apple's fallback discovery, `mdm-byod`/`mdm-adde`, signed plist body `LANGUAGE`/`PRODUCT`/`VERSION`, 401 `WWW-Authenticate: Bearer method="apple-as-web", url=...`, `user-identifier` on the web-auth URL, 308 to `apple-remotemanagement-user-login://authentication-results?access-token=`, second POST with `Authorization: Bearer`)
- Doc: <https://developer.apple.com/documentation/devicemanagement/implementing-the-oauth2-authentication-account-driven-enrollment-flow> (`method="apple-oauth2"`, `authorization-url`, `token-url`, `redirect-url` with scheme `apple-remotemanagement-user-login`, `client-id`, `scope`, `login_hint`, authorization code grant, refresh tokens)
- Doc: <https://developer.apple.com/documentation/devicemanagement/implementing-the-enrollment-sso-flow>, <https://developer.apple.com/documentation/devicemanagement/enrollmentssodocument>
- YAML: `third_party/device-management/mdm/errors/well-known.failed.yaml` (403 `com.apple.well-known.failed`), `mdm/profiles/com.apple.mdm.yaml` (`AssignedManagedAppleID`, `EnrollmentMode` `BYOD|ADDE`, `RequiredAppIDForMDM`, `ManagedAppleID` removed in iOS 18/macOS 15), `mdm/checkin/gettoken.yaml` and `tokenupdate.yaml` (`EnrollmentID` required and `UDID` forbidden for user enrollments), `other/esso.yaml`

## References read

- `vbnin/Apple-JSON-discovery-server@da4599b` `default-ssl.conf`, `json_files/`, `README.md`
- `fleetdm/fleet@b44343c` `server/service/handler.go:1425-1455` (service discovery), `server/service/apple_mdm.go:2785-2882` (bearer flow, 400 for unsupported products), `ee/server/service/apple_mdm.go`, `ee/server/service/mdm.go:1039-1061` (single-use challenge), `server/mdm/apple/apple_mdm.go:1332-1412` (BYOD profile), `docs/Contributing/product-groups/mdm/apple-account-driven-user-enrollment.md`
- `zentralopensource/zentral@b10dd22` `zentral/contrib/mdm/public_views/user.py` (308 hand-back, discovery JSON)
- Record 0026 (DEP account-driven discovery assignment), 0009 (enrollment profile builder), 0016 (user auth state)

## Amendment: protocol alignment and evidence (2026-09-07)

Record [0047](0047-enrollment-authentication-and-optional-security-services.md)
supersedes this record's original token design and comparative claims. The original
single-use profile bearer plus long-lived query credential did not implement Apple's
ongoing bearer behavior. Claims that particular reference projects necessarily fail
our tests were not established by running equivalent requests against those projects
and have been removed.

The current comparison uses the three pinned checkouts in record 0047. Zentral's
persistent UserEnrollmentSession and certificate subject/session association inform
our enrollment association design. Fleet's configurable admission and atomic GCRA
implementation inform the separation of quotas from ownership policy. MicroMDM's
certificate depot and pin checks illustrate why a pin alone is insufficient for
revocation. These observations do not establish general superiority over those
projects, which have different product scopes and operational experience.

## Decision

1. Discovery remains a configurable router serving Apple's `mdm-byod` and `mdm-adde`
   versions, exact model-family parsing, validated HTTPS URLs and documented errors.
2. The signed profile POST uses an injectable body parser and token verifier. A
   successful profile fetch does not consume its access bearer. Access tokens remain
   usable until expiry, invalidation or refresh replacement. Profile URLs carry no
   proprietary enrollment credential.
3. Authorization codes bind client, redirect URI and scope. The token endpoint checks
   that metadata before atomically consuming the code and creating access/refresh
   tokens. Refresh consumption, rotation and old-access invalidation are one
   transaction. `expires_in` reflects the actual access-token lifetime.
4. Each profile issuance records its authenticated subject, identity-provider issuer,
   Managed Apple Account, origin and product. A random reference is available to the
   profile hook through `AssociationFromContext`. Issuers register the exact certificate
   fingerprint against that association before returning a certificate. The reference
   and certificate subject alone are not authorization.
5. First Authenticate atomically reserves the device-generated identity before service
   writes, then confirms the association after service success. A failed downstream
   write leaves a reservation that only the same identity can retry; it does not
   authorize ongoing requests. This is a two-stage protocol across independently
   injectable stores, not one distributed transaction. Multiple devices per account
   remain supported, and email addresses are never compared to UDIDs/EnrollmentIDs.
6. Every relevant check-in, connect and DDM operation verifies the association. Bearer
   identity is required on iOS/iPadOS/visionOS channels and macOS user channels. Apple
   omits the bearer on the macOS device channel for both BYOD and ADDE. Traditional
   device channels are selected by their enrollment origin, not assumed to be ADDE.
7. A typed account reauthentication result maps to 401 with Apple's challenge. Ordinary
   certificate/pinning errors keep their existing behavior. The simulator retains
   bearer/refresh credentials and retries the exact interrupted request once.
8. Memory and SQLite/PostgreSQL/MySQL share atomic token and association semantics.
   Token values are stored only by SHA-256 digest. Applications may inject an external
   IdP verifier and a separate association store. The reference server supplies both
   on its process database and binds account SCEP challenges to a profile and CSR.
9. The device-facing OAuth flow accepts Apple's documented request shape without
   requiring PKCE. The upstream OIDC web flow retains its existing S256 protection.
   DEP admission remains optional caller policy through ADE's existing hooks.

## Verification

- `TestBearerIdentityAndChannelRules`: BYOD/ADDE, Apple platform exceptions, all ongoing
  operations, subject/account mismatch, expired bearer and unregistered certificate.
- `TestAssociationClaimRaceAndChallenge`: one winning device identity, retry-safe CSR
  challenge binding and expiry.
- `TestOAuthMetadataBeforeConsumeAndConcurrentRotation`: wrong clients/redirects/scopes
  do not consume grants; refresh races have one winner; old access tokens stop working.
- `TestReauthenticationRetainsInterruptedCommandResult`: the simulator retries the
  same command UUID and acknowledgement, retaining enrollment state.
- `TestPKIAndAccountStatePersistAcrossInstances`: an independently built reference
  server reads the same tokens and certificate/enrollment associations from SQLite.

Tests establish these code properties. Physical-device interoperability still needs
Apple hardware and an appropriately configured identity provider.
