# 0016: UserAuthenticate challenge and token state

## Context

A macOS user channel can perform a two-message `UserAuthenticate` exchange before its first `TokenUpdate`.

## Decision

`DigestUserAuth` stores a random, expiring challenge in `user_auth`, keyed by the user enrollment and linked to its parent device. `UserVerifier` validates the response. `HA1Verifier` implements the RFC 2617 MD5 digest calculation with constant-time comparison. A successful exchange stores a random authentication token; failed or expired challenges are cleared. Unmanaged users receive 410, and infrastructure failures remain internal errors.

## Rationale

The separate table supports a handshake that precedes creation of the user enrollment row. A verifier interface allows deployments to replace digest validation without changing storage or transport handling.

## Constraints

The digest parameters and wire interoperability have not been validated against a physical macOS client. The stored token is evidence of a completed handshake; the service does not validate an undocumented token carrier on later requests. Optional `RequireUserAuth` gates ordinary macOS user-channel `TokenUpdate` on that stored state. Shared iPad and User Enrollment user channels are exempt.

## Verification

Service tests cover successful, malformed, expired and rejected responses, verifier failures and event publication. Storage contracts cover parent validation, cleanup, copies and token persistence. Simulator tests independently exercise digest construction.

## References

- [server/service/userauth.go](../../../server/service/userauth.go)
- [server/service/userauth_test.go](../../../server/service/userauth_test.go)
- [storage/storagetest](../../../storage/storagetest)
- [simulator/digest.go](../../../simulator/digest.go)
- <https://developer.apple.com/documentation/devicemanagement/check-in>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm@4948319`, `service/nanomdm/ua.go`, `storage/kv/mdm.go`, `storage/mysql/mysql.go`, `StoreUserAuthenticate`
- `micromdm/micromdm@904493b`, `mdm/checkin.go`, `mdm/server.go`
- `fleetdm/fleet@b44343c`, `server/mdm/nanomdm/service/nanomdm/ua.go`, `server/mdm/nanomdm/service/multi/multi.go`
- `zentralopensource/zentral@b10dd22`, `zentral/contrib/mdm/public_views/mdm.py`, `zentral/contrib/mdm/models.py`, `EnrolledUser`
