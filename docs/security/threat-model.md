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
| `/checkin`, `/connect` | Spoofing an enrollment | `Mdm-Signature` CMS verification chained to the enrollment CA, or mTLS; identity hash pinned per enrollment; rotation only on `Authenticate` for the same UDID with a chained cert | `TestMdmSignatureRejectsUnpinned`, `TestCertRotationPolicy` (phase 2) |
| `/checkin`, `/connect` | Tampering with message bodies | Signature covers the body; size and depth limits before decode | `TestBodyLimits`, fuzz targets (phase 2) |
| `/checkin` | Replay of `Authenticate` to reset state | Re-enrollment policy hook; transactional cleanup with audit event | `TestReenrollPolicy` (phase 2) |
| `/checkin` | Signing time skew rejected or accepted too loosely | Configurable clock skew, default 5 minutes | `TestVerifyClockSkew` (phase 2) |
| DDM `/declaration/*` | Information disclosure of another enrollment's declarations | Enrollment resolved from the verified identity, never from a body field | `TestDeclarationScopedToEnrollment` (phase 5) |
| DDM proxy | Spoofed proxy calls | HMAC-SHA256 over body and timestamp, constant-time compare, replay window with nonce store | `TestProxyHMACReplay` (phase 5) |
| `/status` (DDM) | Denial of service via oversized or deeply nested reports | Body limits, depth limits, streaming parse | `FuzzStatusReportDecode` (phase 5) |
| SCEP | Unauthorised certificate issuance | One-time challenges bound to enrollment; CSR verifier hook | `TestSCEPChallengeOneTime` (phase 3) |
| ACME `device-attest-01` | Forged attestation | Chain to Apple Enterprise Attestation Root, freshness, nonce, policy hook | `TestAttestationRejectsBadChain` (phase 7) |
| OTA `profile-service` | Forged phase 1 device attributes | Verify signature against the Apple iPhone Device CA | `TestOTAPhase1Verify` (phase 3) |
| Service discovery | Leaking enrollment URLs to unauthenticated callers | Documented as public by Apple; only routing data served | review |
| Push | Credential exposure | Push key held via `secrets.Provider`, never logged, redacted `String()` | `TestSecretsRedacted` (phase 3), gosec G101 |
| Admin API | Elevation of privilege | Reference server requires API token; library exposes hooks for authz | `TestAdminAuth` (phase 8) |
| Storage | Tampering, disclosure | Parameterised SQL only; secrets encrypted at rest through the provider; migrations owned by one package | `sqlcommon` review, gosec G201/G202 |

## Repudiation

Every state change emits a typed event with enrollment id, actor (device or admin), and timestamp; the audit hook persists them. Proof: `TestAuditHookRecordsAllEvents` (phase 2).

## Out of scope

- Compromise of the device itself.
- Compromise of Apple services.
- TLS termination configuration of the deployment (documented in the deployment guide, phase 8).

## Review log

| Date | Phase | Reviewer | Notes |
|---|---|---|---|
| 2026-09-01 | 0 | initial | Model created from the plan; controls and proofs are targets until the named tests exist. |
