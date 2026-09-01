# Threat model

Status: living document, started 2026-09-01 (phase 0). Updated at the end of every phase.
Method: STRIDE per endpoint. Each row names the control and the test or review that proves it.

## Assets

- Enrollment identities (device certificates), push tokens and push magic.
- APNs push certificate and private key.
- DEP and ABM tokens; SCEP challenges; ACME account keys; CA signing keys.
- Declarations and profiles (may contain credentials such as SCEP challenges or Wi-Fi passwords).
- Bootstrap tokens and unlock tokens (macOS Secure Token escrow).
- Status reports (device inventory, certificate lists, account lists).

## Trust boundaries

1. Device to server over TLS (check-in, connect, DDM, SCEP, ACME, OTA enrollment, service discovery).
2. Server to Apple (APNs, DEP, ABM, GDMF).
3. Admin API and CLI to server.
4. Server to storage.
5. MDM core to DDM proxy (when split across processes).

## Endpoints

| Endpoint | Threat | Control | Proof |
|---|---|---|---|
| `/checkin`, `/connect` | Spoofing an enrollment | `Mdm-Signature` CMS verification chained to the enrollment CA (`httpapi.CertFromMdmSignature`), or mTLS/proxy header; identity fingerprint pinned per enrollment; rotation only on `Authenticate` with a chained cert, published as `CertRotated` | `service.TestPinningAndReenroll`, `service.TestAuthorizeGuardsEveryMessage`, `e2e.TestE2E_Reenroll` |
| `/checkin`, `/connect` | Tampering with message bodies | Signature covers the body; size and depth limits before decode | `httpapi.TestCertMiddlewares` (tampered body), `httpapi.TestCheckinAndConnectHandlers` (oversized), `plist.FuzzUnmarshal`, `mdm.FuzzDecodeCheckin`, `mdm.FuzzDecodeResponse`, `cms.FuzzVerify` |
| `/checkin` | Replay of `Authenticate` to reset state | Re-enrollment policy (`AllowReenroll`/`DenyReenroll`/custom); transactional cleanup of queue, tokens, and pin | `service.TestDenyReenrollAndPinModes`, `storagetest.RunEnrollmentSuite/ReenrollClearsState` |
| `/checkin` | Signing time skew rejected or accepted too loosely | Configurable clock skew in `cms.VerifyOptions`, tolerant path re-verifies digest and signature | `cms.TestVerifySigningTimeSkew`, `cms.TestTolerantErrorBranches` |
| DDM `/declaration/*` | Information disclosure of another enrollment's declarations | Enrollment resolved from the verified identity, never from a body field | `TestDeclarationScopedToEnrollment` (phase 5) |
| DDM proxy | Spoofed proxy calls | HMAC-SHA256 over body and timestamp, constant-time compare, replay window with nonce store | `TestProxyHMACReplay` (phase 5) |
| `/status` (DDM) | Denial of service via oversized or deeply nested reports | Body limits, depth limits, streaming parse | `FuzzStatusReportDecode` (phase 5) |
| SCEP | Unauthorised certificate issuance | Challenge providers: one-time (consumed on first use, expiring) and HMAC bound to the CSR subject; renewals accepted only from certificates that chain to our CA; CSR verifier hook; CA policy on key size, usage, SANs | `scep.TestChallenges`, `scep.TestRenewalSkipsChallenge`, `scep.TestCSRVerifierVeto`, `ca.TestSelfSignedAndLocalSignsWithPolicy`, `e2e.TestE2E_SCEPEnrollPush` |
| SCEP | Oversized or malformed PKI messages | Body limit, signed failure CertRep for policy rejections, 400 for unparseable envelopes | `scep.TestCACertBundleAndHandlerErrors`, `scep.TestHandlerBodyAndClientTransportErrors` |
| ACME `device-attest-01` | Forged attestation | Chain to Apple Enterprise Attestation Root, freshness, nonce, policy hook | `TestAttestationRejectsBadChain` (phase 7) |
| OTA `profile-service` | Forged phase 1 device attributes | Attached CMS signature verified against the Apple iPhone Device CA pool; phase 2 classified by the enrollment CA pool so the challenge cannot be skipped by signing with an unrelated certificate; body limit; `Authorize` hook | `enroll.TestOTAPhase1Verify`, `e2e.TestE2E_OTAProfileService` |
| Enrollment profile | Tampered profile installs a different server | Attached CMS signing (`profile.Sign`) verified on parse with `RequireSignature`; https-only URLs; schema validation | `profile.TestSignAttached`, `enroll.TestBuildErrors` |
| Service discovery | Leaking enrollment URLs to unauthenticated callers | Documented as public by Apple; only routing data served | review |
| Push | Credential exposure | Push certificates loaded through `push.CertStore`; secrets carried as `secrets.Secret`, which redacts in `fmt`, `slog`, and JSON; providers are `os.Root`-scoped for files | `secrets.TestSecretsRedacted`, `secrets.TestProviders`, gosec G101 |
| Push | Stale or invalid tokens used to wake the wrong device | 410 marks the token invalid and publishes `PushTokenInvalid`; per-topic clients reload on certificate change and refuse expired certificates | `push.TestNotifierPublishesInvalidToken`, `push/apns.TestPerTopicClientsAndExpiry`, `e2e.TestE2E_PushInvalidToken` |
| Admin API | Elevation of privilege | Reference server requires API token; library exposes hooks for authz | `TestAdminAuth` (phase 8) |
| Storage | Tampering, disclosure | Parameterised SQL only; secrets encrypted at rest through the provider; migrations owned by one package | `sqlcommon` review, gosec G201/G202 |
| `/checkin`, `/connect` | Unenrolling devices by mistake | Handlers never return 401; Apple's unrecognized-device body only with `httpapi.Config.UnenrollUnknown` | `httpapi.TestServiceErrorMapping` |

## Repudiation

Every state change emits a typed event with enrollment id, actor (device or admin), and timestamp on the `event.Bus`; subscribers persist them. Proof: `service.TestEnrollAndCommandFlow` asserts the full event sequence.

## Out of scope

- Compromise of the device itself.
- Compromise of Apple services.
- TLS termination configuration of the deployment (documented in the deployment guide, phase 8).

## Review log

| Date | Phase | Reviewer | Notes |
|---|---|---|---|
| 2026-09-01 | 0 | initial | Model created from the plan; controls and proofs are targets until the named tests exist. |
| 2026-09-01 | 2 | protocol core | Check-in, connect, signature verification, pinning, and re-enrollment controls implemented; proofs point at real tests. |
| 2026-09-01 | 3 | push, SCEP, enrollment | SCEP, OTA profile service, enrollment profile signing, push token invalidation, and secret redaction controls implemented; the SCEP endpoint and OTA endpoint are new trust boundaries. |
