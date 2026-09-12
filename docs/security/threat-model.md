# Threat model

This review groups spoofing, tampering, repudiation, disclosure, denial of service and privilege
escalation risks by trust boundary. Controls describe the implemented library and reference
composition. Optional controls apply only when configured; source and test links identify the
scope of the evidence rather than proving deployment security.

## Assets and boundaries

Protected data includes enrollment identities, CA and APNs private keys, device push and escrow
tokens, Apple service credentials, account grants, admin credentials, profiles, declarations,
status reports and audit records. Profiles and declarations can contain credentials.

Trust boundaries are device-to-server TLS; enrollment browser-to-identity-provider OIDC;
server-to-Apple APIs; admin client-to-server; process-to-database; the optional MDM-to-DDM proxy;
and process-to-event sinks. A certificate header is trustworthy only when a controlled proxy
verifies possession and external clients cannot supply or overwrite it.

## Device protocol and enrollment

| Risk | Implemented control and limit | Evidence |
|---|---|---|
| Forged device identity | CMS, mutual TLS or a verified proxy certificate establishes transport identity; the service pins certificate fingerprints. Transport trust must be configured by the integrator. | [HTTP middleware](../../server/httpapi/cert.go), [service authorization](../../server/service/service.go) |
| Tampered or oversized messages | CMS covers signed bodies. HTTP body bounds and XML plist depth limits constrain decoding. A certificate header alone does not sign a body. | [HTTP API](../../server/httpapi/), [plist decoder](../../devicemanagement/mdmprotocol/plist/) |
| Unauthorized replacement certificate | The reference server denies re-enrollment with a changed certificate by default. The reusable service also denies it by default; atomic pinning prevents partial resets and same-certificate retries preserve state. Certificate reuse across enrollment identifiers is denied by default. | [Service policies](../../server/service/service.go), [reference configuration](../../server/internal/app/app.go) |
| Cross-account enrollment or replay | Trusted issuance registers a certificate/account association. The first `Authenticate` reserves and then confirms an enrollment identifier. Failed storage permits retry with the same identifier. A certificate subject alone cannot register issuance. | [Account associations](../../devicemanagement/mdmprotocol/enroll/accountdriven/associations.go), [issuer integration](../../server/internal/app/security.go) |
| Stolen or expired account bearer | Access tokens remain reusable until expiry or invalidation. Codes and refresh tokens have atomic exchange semantics. Recognized account enrollments receive reauthentication challenges; macOS device-channel requests follow the bearer exception. Custom verifiers must validate issuer, audience, validity and stable identity claims. | [Account-driven enrollment](../../devicemanagement/mdmprotocol/enroll/accountdriven/), [operations](../operations/enrollment-security.md) |
| Browser authentication replay | OIDC uses state, nonce and PKCE protections. Per-flow Secure host cookies bind state to the initiating browser. Shared SQL state supports cross-replica completion; GET alone can consume it. Known JWKS keys expire after 15 minutes. | [Web authentication](../../devicemanagement/mdmprotocol/enroll/webauth/) |
| Forged ADE information | Signed `MachineInfo` verification is strict by default. Audit mode permits unverified input and is diagnostic policy. Explicit organizational device/account admission is required; empty policy denies issuance. DEP admission uses configured accounts and rejects deleted or removed assignments. | [ADE](../../devicemanagement/mdmprotocol/enroll/ade/), [ADE tests](../../server/e2e/ade_test.go) |
| User-channel authentication misuse | Digest challenges expire and are consumed. The optional `RequireUserAuth` gate checks stored token presence on eligible `TokenUpdate`; it does not verify a token supplied with that request. Shared iPad and User Enrollment have separate handling. | [User authentication](../../server/service/userauth.go), [check-in dispatch](../../server/service/checkin.go) |
| Unintended erasure | `ReturnToService` is disabled by default. Enabling the policy authorizes the device's erasure/re-enrollment response and requires appropriate enrollment eligibility. | [Return to Service tests](../../server/e2e/returntoservice_test.go) |

## PKI and admission

| Risk | Implemented control and limit | Evidence |
|---|---|---|
| Unauthorized SCEP issuance or renewal | Explicit challenge policy is required. Reference random grants expire within one hour and bind to the first valid CSR; retry returns the same certificate. Admission is rechecked before issuance; status checks precede renewal. | [SCEP](../../devicemanagement/pki/scep/), [issuer integration](../../server/internal/app/security.go) |
| ACME request replay or account substitution | JWS verification, single-use nonces, account/order bindings and CSR checks protect the exchange. Client identifiers have configured lifetime and policy. | [ACME](../../devicemanagement/pki/acme/) |
| Concurrent ACME issuance or incomplete registration | Cross-instance order transactions serialize state transitions. A CSR-bound receipt is persisted before idempotent registration; download remains blocked until completion. Delayed challenge results cannot reopen a completed order. Pure signing callbacks and shared issuer configuration are required. | [Issuance](../../devicemanagement/pki/acme/issuance.go), [race and recovery tests](../../devicemanagement/pki/acme/issuance_security_test.go), [store contract](../../devicemanagement/storage/acme/acmetest/order_security.go) |
| Forged device attestation | Verification checks configured anchors, freshness, device properties and requested key binding. Unattested issuance requires explicit policy; hardware interoperability needs physical-device tests. | [Attestation verifier](../../devicemanagement/pki/acme/attest/), [decision 0032](../research/decisions/0032-managed-device-attestation.md) |
| Continued use of revoked certificates | The default-enabled reference issuer registry records issuance before returning certificates and checks status independently of pin mode. Unknown, expired and revoked certificates fail enforcement. Checkout and unpinning do not revoke. | [Revocation](../../devicemanagement/pki/revocation/), [service status tests](../../server/service/) |
| Inconsistent CRL/OCSP publication | Issuer locks serialize signed CRL publication with its number. OCSP reports unknown for unregistered serials. Retired issuer signing keys must remain available while status responses are required. | [Registry](../../devicemanagement/pki/revocation/), [operations](../operations/enrollment-security.md) |
| Request floods or forged peer keys | Optional quotas atomically account for per-peer and aggregate buckets using bounded state; SQL uses database time after locking. Socket peers are the default identity. Forwarded addresses require trusted CIDRs. Quota exhaustion returns 429; capacity/database failure returns 503. Health checks are exempt. | [Rate limiter](../../devicemanagement/ratelimit/), [protocol SQL state](../../server/statestore/), [reference configuration](../../server/internal/app/security.go) |

## DDM, administration and external services

| Risk | Implemented control and limit | Evidence |
|---|---|---|
| Cross-enrollment declaration disclosure | The service supplies the authorized enrollment identity. The engine serves versions from that enrollment's snapshot. | [DDM engine](../../devicemanagement/mdmprotocol/ddm/), [DDM scenarios](../../server/e2e/ddm_test.go) |
| Invalid declarations or excessive status input | Upload parsing, generated validation and predicate parsing precede storage. Status processing has byte/depth/path/item bounds and rejects malformed JSON. Raw report retention is bounded by configuration. | [DDM engine](../../devicemanagement/mdmprotocol/ddm/), [predicate grammar](../../devicemanagement/mdmprotocol/ddm/predicate/doc.go) |
| Forged private proxy traffic | The reference server requires HMAC keys in both directions. Versioned envelopes bind method, target, timestamp, nonce, content type and body, and responses to the request. HTTPS is required. Shared atomic nonce claims prevent replay across replicas. Library mutual-TLS and bearer options require custom composition. | [Proxy adapters](../../server/ddmadapter/), [decision 0023](../research/decisions/0023-ddm-adapters-and-wire-contract.md) |
| Stale DDM state after checkout | The service hook clears associated declarative state and pending synchronization work. | [DDM synchronization](../../server/ddmsync/), [checkout scenario](../../server/e2e/ddm_test.go) |
| Administrative privilege escalation | Stored principals and scoped tokens use Cedar authorization against the canonical stored channel and parent, with immutable identity guards in all stores. `DM_ADMIN_TOKEN` bypasses policy and remains accepted until removed and the process restarted. | [Admin authorization](../../server/adminauth/), [admin route enforcement](../../server/internal/app/adminauthz.go) |
| Credential delegation bypass or root lockout | Root-only principal/credential mutation prevents claiming authority through a named Cedar principal or a context-dependent policy. Store transactions preserve an active root under concurrent mutations. Natural expiry and policy lockout still require operator management. | [Manager](../../server/adminauth/manager.go), [admin store contracts](../../server/adminauth/adminauthtest/security.go) |
| Plaintext administration or webhook disclosure | The runtime defaults to loopback and requires TLS remotely. The CLI rejects remote HTTP and TLS verification bypasses. Webhooks require HTTPS, support private CA trust, refuse redirects and redact URL-bearing transport errors. Custom embedded transports remain a trusted boundary. | [Runtime](../../server/internal/runtime/runtime.go), [admin client](../../server/internal/dmctl/adminclient/adminclient.go), [webhook](../../server/eventsink/webhook.go) |
| Apple credential disclosure or retry amplification | Clients refuse credential-bearing redirects, redact credential values and implement service-specific token refresh and retry behavior. Endpoint and trust configuration remain deployment responsibilities. | [Apple clients](../../devicemanagement/appleplatformservices/), [credential storage](../../server/axmcreds/) |
| Misinterpreted push failure | Only the service's invalid-token classification emits the invalid-token event; transient failures remain retryable outcomes. Push failure is not evidence that an enrollment should be deleted. | [Push notifier](../../server/pushnotify/), [decision 0042](../research/decisions/0042-push-failure-classification.md) |

## Storage, audit and residual risks

Credential-bearing columns and raw enrollment/command/result records are sealed
with AES-256-GCM under named keys. DDM declaration versions and snapshots and
protocol state are also encrypted. Authenticated data binds ciphertext to its
purpose and canonical row identity. Persistent reference storage requires a
keyring; rotation rewraps values with concurrent-write guards. This does not
encrypt database metadata, raw DDM status, audit records or plaintext exports.
Protect database access, transport and backups and treat in-process event data
as sensitive.

Event sinks publish explicit projections instead of serializing raw event payloads. Unknown
event types expose metadata only. Persistent audit is optional and supports append and retention
pruning; it does not prevent a privileged database operator from changing records. Audit delivery
and storage failures need operational monitoring. See [event sinks](../../server/eventsink/),
[audit storage](../../server/audit/) and [column sealing](../../devicemanagement/storage/crypt/).

Memory storage loses credentials and security state on restart. Replicas need shared SQL state,
issuer material and security settings. Account association confirmation crosses separate stores;
its reservation protocol supports retry but is not a distributed transaction. Quota capacity
accounting serializes writers in a namespace and needs deployment load testing.

Device compromise, Apple service compromise, TLS termination, network denial-of-service
protection, backup recovery and physical-device compatibility are outside the automated test
model. Revocation is enabled by default; quotas require explicit configuration. Admission
policy and cached DEP inventory must remain current on every replica. Follow
[enrollment security operations](../operations/enrollment-security.md).

The [2026-09-11 hardening record](../wip/apple-conformant-security-hardening-2026-09-11.md)
separates Apple requirements, Apple-permitted issuance policy and infrastructure
controls, with reproducible regressions and a physical-device checklist.
