# 0028: Account-driven enrollment and service discovery

## Context

Account-driven Device Enrollment and account-driven User Enrollment begin with a Managed Apple Account and service discovery, then require authentication and an enrollment identity.

## Decision

Discovery maps a model family and user identifier to an HTTPS enrollment URL. Account-driven handlers implement Apple's `apple-as-web` and `apple-oauth2` authentication flows, returning a bearer challenge when authentication is required.

Access tokens are reusable until expiry or invalidation. Codes and rotating refresh tokens are consumed atomically after validating their client, redirect and scope metadata. Profile issuance creates a random reference associated with subject, issuer, Managed Apple Account and platform. Only trusted issuance can register the certificate. The first successful `Authenticate` confirms the reserved enrollment identifier.

## Rationale

A certificate proves possession of the issued key; its association binds that key to the authenticated account and profile. Neither a bearer nor an asserted certificate subject alone establishes the complete enrollment identity.

## Constraints

macOS device channels omit the ongoing bearer; macOS user channels and supported iOS, iPadOS and visionOS channels send it. Recognized account-driven sessions can be challenged to reauthenticate and retry. Legacy query-credential profiles need re-enrollment. Apple-facing OAuth does not require extra PKCE parameters; upstream OIDC uses S256. Record 0047 and the operations guide describe shared state and migration.

## Verification

Tests cover wrong-account and cross-certificate replay, channel rules, association races, reusable access tokens, grant rotation, infrastructure failures and interrupted-request retries.

## References

- [mdmprotocol/enroll/accountdriven](../../../devicemanagement/mdmprotocol/enroll/accountdriven)
- [mdmprotocol/enroll/discovery](../../../devicemanagement/mdmprotocol/enroll/discovery)
- [server/internal/app/enroll.go](../../../server/internal/app/enroll.go)
- <https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment>
- <https://developer.apple.com/documentation/devicemanagement/get-.well-known-com.apple.remotemanagement>
- <https://developer.apple.com/documentation/devicemanagement/implementing-the-simple-authentication-account-driven-enrollment-flow>
- <https://developer.apple.com/documentation/devicemanagement/implementing-the-oauth2-authentication-account-driven-enrollment-flow>
- <https://developer.apple.com/documentation/devicemanagement/implementing-the-enrollment-sso-flow>
- <https://developer.apple.com/documentation/devicemanagement/enrollmentssodocument>

Reference source identifiers and paths (relative to the named project):

- `vbnin/Apple-JSON-discovery-server@da4599b`, `default-ssl.conf`, `json_files/`, `README.md`
- `fleetdm/fleet@b44343c`, `server/service/handler.go:1425-1455`, `server/service/apple_mdm.go:2785-2882`, `ee/server/service/apple_mdm.go`, `ee/server/service/mdm.go:1039-1061`, `server/mdm/apple/apple_mdm.go:1332-1412`, `docs/Contributing/product-groups/mdm/apple-account-driven-user-enrollment.md`
- `zentralopensource/zentral@b10dd22`, `zentral/contrib/mdm/public_views/user.py`
