# 0016: UserAuthenticate challenge and token state

Status: accepted (the `HA1Verifier` implementation remains unverified against a real macOS client; see the open points)
Date: 2026-09-02
Phase: 4

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> (`UserAuthenticate`: the server answers the first message with a `DigestChallenge`, the second with an `AuthToken`; 410 means the server does not manage the user)
- YAML: `third_party/device-management/mdm/checkin/userauthenticate.yaml` (`UserID`, `DigestResponse`; no response keys are declared)
- YAML: `third_party/device-management/mdm/checkin/tokenupdate.yaml` (the user channel `TokenUpdate` that follows a successful handshake)
- RFC 2617 (HTTP Digest, MD5) and RFC 7616 (Digest with SHA-256): the `DigestResponse` the client returns is a Digest `Authorization` value

## References read

- `micromdm/nanomdm@4948319` `service/nanomdm/ua.go`, `storage/kv/mdm.go`, `storage/mysql/mysql.go` (`StoreUserAuthenticate`)
- `micromdm/micromdm@904493b` `mdm/checkin.go`, `mdm/server.go` (410 branch), PR #379
- `fleetdm/fleet@b44343c` `server/mdm/nanomdm/service/nanomdm/ua.go`, `server/mdm/nanomdm/service/multi/multi.go`
- `zentralopensource/zentral@b10dd22` `zentral/contrib/mdm/public_views/mdm.py`, `zentral/contrib/mdm/models.py` (`EnrolledUser`)

## Known pitfalls found

- Every reference (NanoMDM, MicroMDM PR #379, Fleet, Zentral) answers 410 or a zero-length challenge; none validates a digest or issues an `AuthToken`.
- NanoMDM: the second `UserAuthenticate` is answered 200 empty unconditionally; the KV backend disables the enrollment on the message while SQL does not.
- Fleet: `multi` runs the `UserAuthenticate` handler twice, once per wrapped service.
- Zentral: `EnrolledUser` is keyed on a globally unique `user_id`, so a reused `UserID` on another device re-points the row.
- Apple's YAML declares no response keys for `UserAuthenticate`; the digest format lives only in prose, so the wire format is unverified against a real client.

## What they do

- **NanoMDM**: `UAService` returns 410 by default; with `-ua-zl-dc` it returns an empty `DigestChallenge` and stores the raw message; the storage method takes the raw plist only.
- **MicroMDM**: `checkin.go` special-cases the message type and `server.go` writes `http.StatusGone`.
- **Fleet**: NanoMDM's `ua.go` unchanged behind `multi`.
- **Zentral**: the check-in view returns 410 for `UserAuthenticate`; `EnrolledUser` rows are created on the user channel `TokenUpdate`.

## What we do better

1. State has its own table `user_auth`, keyed by the user enrollment id with a foreign key to the parent device, because the handshake precedes the user's `TokenUpdate`. Device channels are refused with `ErrInvalid`, a missing parent with `ErrNotFound`, and a device re-enrollment deletes its users' rows.
2. `service.DigestUserAuth` implements the two-message flow: the first message gets `Digest realm="<realm>", nonce="<16 random bytes hex>", qop="auth", algorithm=MD5` (realm defaults to `DefaultUserAuthRealm`, "mdm"); the challenge is one-shot with a TTL of `DefaultUserAuthChallengeTTL` (5 minutes) and is cleared on failure or expiry, so a captured `DigestResponse` cannot be replayed. `simulator.DigestResponse` and `simulator.HA1` build the client side for tests.
3. Verification is behind `UserVerifier` (given a `VerifyInput` of user id, realm, challenge, and `DigestResponse`); `HA1Verifier` implements RFC 2617 with a constant-time compare and a `#nosec G401` justification for MD5. A malformed or mismatched response is `ErrBadDigest` and is treated as a rejected login; any other verifier error maps to `CodeInternal`; `Manage` returning `ErrUserNotManaged` maps to 410 (`CodeGone`), so the reference behaviour remains available.
4. Success issues a 32-byte hex `AuthToken` that is stored sealed (0013) and exposed through `UserAuth`; events `UserAuthenticated` and `UserAuthFailed` carry the outcome. Storing a token without a prior challenge fails with `ErrNotFound`.
5. Both raw plists are retained, `ClearUserAuth` is idempotent, and reads return copies, so a caller cannot mutate stored state.

Open points: the `AuthToken` carrier on later requests is not documented, so the token is stored and exposed but does not yet gate `authorize()`. The `HA1Verifier` implementation (RFC 2617 MD5 parameters, `POST` as the method in HA2, and the `qop="auth"` shape of the challenge) remains unverified against a real macOS client; the storage and handler flow are accepted on their own tests, and the verifier is swappable through `UserVerifier` if the real client differs.

## Verified by

1. `storagetest.RunUserAuthSuite/DeviceChannelInvalid`, `RunUserAuthSuite/ParentMissing`, `RunUserAuthSuite/ClearedOnDeviceReenroll`, `RunUserAuthSuite/ChallengeAndTokenRoundTrip` (prove claim 1; would fail on Zentral because the row is keyed by `user_id` alone, and on NanoMDM because SQL never clears it).
2. `service.TestDigestUserAuthFlow/ChallengeDiffersPerCall`, `TestDigestUserAuthFlow/Expired`, `TestDigestUserAuthFlow/WrongDigest`, `TestDigestUserAuthFlow/MalformedDigest` (wrong or malformed digest returns an empty token, clears the challenge, publishes `UserAuthFailed`), `simulator.TestDigestResponse`, `simulator.TestHA1`, `simulator.TestDigestResponseFailures` (prove claim 2; would fail on every reference because no challenge is issued).
3. `service.TestHA1Verifier`, `service.TestDigestUserAuthFlow/VerifierError` (`CodeInternal`), `TestDigestUserAuthFlow/Manage` (`ErrUserNotManaged` is `CodeGone`, any other error `CodeInternal`), `TestDigestUserAuthFlow/BadRequests` (device channel, nil request or message, invalid id are `CodeBadRequest`), `TestDigestUserAuthFlow/UnknownParent` (`CodeUnknownEnrollment`), `service.TestDigestUserAuthCore` (the handler wired into `Core.Checkin`) (prove claim 3).
4. `storagetest.RunUserAuthSuite/ChallengeAndTokenRoundTrip` (a token without a prior challenge is `ErrNotFound`; a new challenge replaces the token), `service.TestDigestUserAuthFlow/RightDigest` (stores a 32-byte hex token and publishes `UserAuthenticated`), `TestDigestUserAuthFlow/NoChallenge` (empty `AuthToken`), `sqlite.TestRawColumnIsNotPlaintext` (`auth_token` sealed) (prove claim 4; would fail on NanoMDM because the second message is answered 200 with no token).
5. `storagetest.RunUserAuthSuite/ChallengeAndTokenRoundTrip` (both raw plists retained, second `ClearUserAuth` succeeds, returned copies do not alias), `service.TestDigestUserAuthFlow/StoreFailures` (all four `UserAuthStore` methods failing are `CodeInternal`), `TestDigestUserAuthFlow/RandFailure`, `TestDigestUserAuthFlow/Defaults` (prove claim 5).

## Rejected alternatives

- Always 410 (every reference): correct for deployments that do not manage users, but it forecloses user channel enrollment; it remains available through `Manage`.
- Zero-length challenge (NanoMDM `-ua-zl-dc`): skips authentication entirely and depends on client behaviour that Apple does not document.
- Keeping the challenge on the enrollment row: the user enrollment does not exist until its `TokenUpdate`, so there is no row yet.
- SHA-256 digest (RFC 7616) as the default: unverified against Apple's client; `algorithm=MD5` is the documented Digest baseline and the verifier is swappable.
