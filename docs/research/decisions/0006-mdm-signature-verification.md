# 0006: Mdm-Signature verification and identity pinning

Status: accepted
Date: 2026-09-01
Phase: 2

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> (`SignMessage` in the MDM payload makes devices send a detached CMS signature of the body in the `Mdm-Signature` header)
- Doc: <https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices>
- YAML: `third_party/device-management/mdm/profiles/com.apple.mdm.yaml` (`SignMessage`)

## References read

- `micromdm/nanomdm@main` `cryptoutil/cryptoutil.go` (`VerifyMdmSignature`), `http/mdm/mdm_cert.go` (extraction middlewares, no 401), `service/certauth/certauth.go` (hash association, retroactive, warn-only)
- `smallstep/pkcs7@main` `verify.go`, `pkcs7.go` (`verifySignatureAtTime`, `SigningTimeNotValidError`, exported signer fields)
- NanoMDM #73

## Known pitfalls found

- NanoMDM #73: a device whose clock lags issues a CMS signing time earlier than its freshly issued certificate's NotBefore, and `pkcs7.Verify` rejects it outright. There is no tolerance option in the library.
- NanoMDM's verifier calls `p7.Verify()` with no trust store: any self-signed certificate that signs the body is accepted; authorization relies entirely on the hash association layer.
- Returning 401 makes some Apple clients unenroll, so NanoMDM never does.

## What they do

- **NanoMDM**: base64 decode, `pkcs7.Parse`, `Content = body`, `Verify()`, `GetOnlySigner`; certificate hash (SHA-256 of DER) associated on `Authenticate`/`TokenUpdate`, checked afterwards; optional retroactive association and warn-only mode.
- **Fleet test client**: `pkcs7.NewSignedData` with `AddSigner` and the enrollment identity.

## What we do better

1. `cms.Verify` accepts a trust store and a clock; chains to the enrollment CA when configured, and when the library reports `SigningTimeNotValidError` within `ClockSkew` of the certificate bounds it re-runs the digest, attribute, chain, and signature checks itself with the tolerance applied.
2. Exactly one signer is required; header decoding errors, parse errors, and mismatched content produce distinct sentinel errors.
3. Identity pinning is in the service with explicit rotation policy and an audited `CertRotated` event rather than boolean flags.
4. Handlers never return 401; unknown enrollments get 403 with Apple's `ErrorUnrecognizedDevice` body only when the deployment opts in.

## Verified by

1. `TestVerifySigningTimeSkew` (future-dated leaf: fails with skew 0, passes with 5m), `TestVerifyChain`.
2. `TestVerifyErrors` (bad base64, garbage DER, two signers, tampered body).
3. `TestCertRotationPolicy` (service).
4. `TestHandlerNever401` (httpapi).

## Rejected alternatives

- Forking `pkcs7` to add a skew option: maintenance burden; the exported signer fields make a local tolerant path feasible.
- Skipping signing time entirely: weakens replay resistance.
