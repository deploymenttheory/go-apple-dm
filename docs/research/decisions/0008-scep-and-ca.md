# 0008: SCEP endpoint and pluggable CA

Status: accepted
Date: 2026-09-01
Phase: 3

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/scep> (SCEP payload keys: URL, Name, Challenge, Subject, Keysize, Key Type, Key Usage, CAFingerprint, Retries, RetryDelay)
- Doc: <https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices>
- YAML: `third_party/device-management/mdm/profiles/com.apple.security.scep.yaml`
- RFC 8894 (SCEP)

## References read

- `smallstep/scep@main` `scep.go` (`ParsePKIMessage`, `DecryptPKIEnvelope`, `Success`, `Fail`, `NewCSRRequest`, `CACerts`, `DegenerateCertificates`)
- `micromdm/scep@main` `server/service.go` (GetCACaps string, GetCACert single vs degenerate, PKIOperation flow), `server/csrsigner.go` (static challenge middleware with constant-time compare and a TODO about renewals), `depot/depot.go` (Depot interface), `csrverifier`
- `jessepeterson/mysqlscepserver` (SQL depot)

## Known pitfalls found

- micromdm/scep's README says the server is basic and unlikely to be supported; challenge validation is a static shared secret compared for every message type, and the code carries a TODO about whether renewals should bypass it.
- The SCEP RA certificate must be RSA: devices encrypt the PKCS#7 envelope to it.
- Serial allocation by counter in a file depot races across processes.
- `HasCN` renewal logic couples the depot to policy.

## What they do

- **micromdm/scep**: `ParsePKIMessage` with CA certs, decrypt envelope with the RA key, `SignCSRContext` chain (challenge middleware, then depot signer with validity days and serial from depot), `Success`/`Fail` with `BadRequest`.
- **smallstep/scep**: the protocol library both use.

## What we do better

1. `ca.Signer` and `ca.Depot` are small interfaces; `ca.Local` signs with an in-memory key and any depot, with a `Policy` (validity, key usages, SAN allow-list) instead of depot-coupled renewal rules; serials are 128-bit random.
2. `scep.Challenge` is an interface with three implementations: static, one-time (bound to an enrollment and consumed on use), and HMAC-derived (stateless, expiring), so an enrollment profile can carry a challenge that cannot be replayed.
3. Renewals (an existing signer certificate that chains to the CA) skip the challenge, resolving the reference TODO explicitly and testably.
4. A `CSRVerifier` hook sees the decrypted CSR and the request context before signing, for policy such as subject or key size checks.
5. `scep.Client` performs GetCACert and PKIOperation so the simulator can enrol exactly like a device.

## Verified by

1. `ca.TestLocalSignsWithPolicy`, `ca.TestSerialsAreRandom`.
2. `scep.TestChallenges` (static, one-time consumed, HMAC expiry).
3. `scep.TestRenewalSkipsChallenge`.
4. `scep.TestCSRVerifierVeto`.
5. `scep.TestClientEnrolls`, `e2e.TestE2E_SCEPEnrollPush`.

## Rejected alternatives

- Depending on micromdm/scep's server: unmaintained by its own admission.
- step-ca as the only CA: too large to embed; kept as an external option through `ca.Signer`.
