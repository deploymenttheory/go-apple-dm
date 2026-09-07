# 0047: Enrollment authentication and optional security services

## Context

Account-driven enrollment requires an ongoing relationship between authentication and the issued identity. Certificate status and traffic admission are separate, optional deployment controls.

## Decision

Reusable access tokens bind subject, issuer and Managed Apple Account to a random profile reference. Trusted issuance registers the certificate. The first `Authenticate` reserves an enrollment identifier before the MDM write and confirms it after success; subsequent requests require the confirmed association. Code and refresh-token exchanges consume grants atomically after metadata validation.

Optional issuer-scoped registration and irreversible revocation support signed CRLs, OCSP and ACME `revokeCert`. Issuance must be persisted before returning a certificate. Status checks run before device service side effects independently of pin mode and apply to SCEP renewals.

Optional GCRA quotas account for peer and aggregate buckets atomically. The transactional byte-record store has in-memory and SQL implementations; SQL locks fixed shards in order and samples database time after locking. Capacity is bounded. Peer identity uses the socket address unless explicitly trusted proxy networks are configured.

## Rationale

Independent certificate and bearer checks prevent a valid credential from authorizing an unrelated enrollment. Shared transactions coordinate grants and quotas across replicas. Optional controls let operators choose certificate lifecycle and traffic policy without imposing an ownership rule on every deployment.

## Constraints

macOS device channels omit ongoing bearer tokens; other supported account-driven channels verify them. Infrastructure errors do not trigger reauthentication. Legacy query-credential profiles require re-enrollment, and existing certificates must be imported before enabling status enforcement. Registry import does not create account associations.

Association reservation/confirmation spans separate stores and is not a distributed transaction; failed writes permit retry with the reserved identifier. The reference OIDC browser handoff remains in memory. Replicas require shared state, issuer keys and configuration. Revocation and quotas are disabled by default; quota capacity accounting serializes writers in its namespace. CheckOut does not revoke a certificate automatically.

## Verification

Tests cover account/certificate replay, channel exceptions, association and token-exchange races, retry of interrupted requests, status checks across transports, independent CRL/OCSP parsing, ACME revocation authorization and shared SQL quotas. Physical Apple-device interoperability remains a separate validation requirement. Configuration and migration steps are in [enrollment security operations](../../operations/enrollment-security.md).

## References

- [mdmprotocol/enroll/accountdriven](../../../mdmprotocol/enroll/accountdriven)
- [pki/revocation](../../../pki/revocation)
- [ratelimit](../../../ratelimit)
- [state](../../../state)
- [server/statestore](../../../server/statestore)
- [server/internal/app/security.go](../../../server/internal/app/security.go)
- <https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment>
- <https://developer.apple.com/documentation/devicemanagement/implementing-the-simple-authentication-account-driven-enrollment-flow>
- <https://developer.apple.com/documentation/devicemanagement/implementing-the-oauth2-authentication-account-driven-enrollment-flow>
- <https://developer.apple.com/videos/play/wwdc2021/10136/>
- <https://www.rfc-editor.org/rfc/rfc6749#section-4.1.3>
- <https://www.rfc-editor.org/rfc/rfc8555#section-7.6>
- <https://www.rfc-editor.org/rfc/rfc5280>
- <https://www.rfc-editor.org/rfc/rfc6960>
