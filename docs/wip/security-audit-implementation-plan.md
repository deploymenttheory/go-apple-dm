# Security audit and implementation plan

Audit baseline: `ecf32ab3f1f9ed664cabf48dc1a26bfeae294b3e`, 10 September 2026.

## Decisions and scope

The audit covers the library, reference server, dependencies, security workflows,
and container configuration. There are no live deployments: correctness takes
priority over API compatibility. Enrollment requires explicit device or account
admission. The reference server enables certificate revocation by default.
Documentation describes the resulting behavior, without migration narratives.

Findings below are based on source inspection. Existing tests passed; new
regression tests must establish the corrected security boundaries. Severity
includes the stated prerequisites and is not a claim of a demonstrated exploit.

## Findings

| ID | Priority | Finding and evidence |
| --- | --- | --- |
| S01 | High | Admin authorization uses caller-supplied channel/ID while storage uses raw ID alone. Channel and parent mismatches can address another policy resource. `server/internal/app/adminauthz.go:436`, `admin.go:211`, `server/sqlstore/sqlcommon/store.go:275`, and `storage/inmem/inmem.go:50`. |
| S02 | High | Authentication resets state before separately pinning a certificate. Concurrent requests or failures expose an unpinned record, and authorization permits retroactive pinning. The reusable service defaults to allowing reenrollment. Requires another transport-accepted certificate. `server/service/checkin.go:102`, `:169`, SQL `UpsertAuthenticate` and `AssociateCert`. |
| S03 | High | Disabled enrollments can poll queued commands; TokenUpdate can reactivate them. Lifecycle checks do not cover all secret/DDM operations. Storage `Disable`, `Next`, `StoreTokenUpdate`, and service authorization. |
| S04 | High | ADE lacks rejecting ownership admission; OIDC accepts any nonempty email. ACME ownership lookup accepts deleted DEP records, searches only one account page, and ignores device lookup failures. `server/internal/app/acme.go:210`, `enroll.go:305`, `:710`. |
| S05 | High | SCEP defaults to NoChallenge in the library. The reference server supplies challenges, but static/HMAC challenges permit issuance with additional keys. `pki/scep/scep.go:109`, `challenge.go:149`. |
| S06 | High | OIDC state/PKCE/nonce are not bound to the initiating browser; a valid callback can be transferred between browsers. HEAD consumes state. Known JWKS keys never expire, and audience/authorized-party checks are incomplete. `mdmprotocol/enroll/webauth/webauth.go:243`, `idtoken.go:241`, `provider.go:138`. |
| S07 | High | Raw TokenUpdate stores UnlockToken outside encrypted columns. Retained commands/results can also contain credentials. Requires access to database contents. `server/sqlstore/sqlcommon/store.go:227`, `seal.go`, `replacement.go:133`. |
| S08 | High, conditional | TLS certificate extraction ignores VerifiedChains; certificate headers have no trusted socket-peer enforcement. Configuring roots selects CMS exclusively and ignores valid direct mTLS. `server/httpapi/cert.go:18`, `server/internal/app/app.go:427`. |
| S09 | Medium, conditional | Private DDM request/response MACs lack freshness and request/response association; HTTP is permitted. `server/ddmadapter/internal/proxywire/proxywire.go`. |
| S10 | Medium | Recursive status flattening has no application depth/path/item limits; configured rate-limit families omit OTA and private DDM. `mdmprotocol/ddm/status.go:51`, app `routeFamily`. |
| S11 | Medium, conditional | Credential-bearing outbound requests follow redirects. Reference OIDC HTTP defaults lack a timeout. DEP clients and `OIDCConfig.client`. |
| S12 | Medium | Security CI omits the nested server module, gosec does not fail findings, and workflow-only edits skip scans. Docker context can include ignored lab credentials. `.github/workflows/security.yml`, `.dockerignore`, `Dockerfile`. |
| A01 | Conformance/security | SCEP helpers cannot disable key extraction; command eligibility assumes supervision, ADE, and user approval. `mdmprotocol/enroll/enroll.go:53`, `server/service/service.go:392`. |

Apple requires association between a device certificate and device identifier.
CMS signing is optional; valid mTLS remains supported. See
[Apple certificate management](https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices).

The inspected attestation code validates the Apple chain, freshness, and final
CSR key association. Preserve these while enforcing organizational admission.
See [Apple attestation validation](https://developer.apple.com/documentation/devicemanagement/validating-a-managed-device-attestation-attestation).

## Implementation order and contracts

### 1. Identity, pinning, and lifecycle — S01–S03

- Keep globally unique raw IDs; require exact channel and parent agreement for
  every operation and reject conflicting upserts/imports. Resolve stored identity
  before Cedar authorization; never trust a caller-supplied parent.
- Add an atomic authentication storage transition covering expected pin,
  certificate history/reuse, enrollment state, and reset. Enforce it across SQL
  connections and memory storage. No retroactive pinning. Default reenrollment to
  denial; use the explicit replacement workflow for identity changes.
- Same-certificate Authenticate retries preserve queues and escrowed material.
- DisabledAt present means disabled; Enabled=false without DisabledAt means
  pending. Only pending enrollment can activate through TokenUpdate. Disable
  cancels queued delivery and gates secrets, DDM, and assets, including children.
  Checkout is idempotent and user checkout does not revoke a shared parent key.

These contracts follow [OWASP authorization guidance](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html).

### 2. Admission, issuance, and revocation — S04–S05

- Shared admission returns a grant with approved identity, mode, and expiry.
  Reference policy supports active DEP assignment or explicit device allowlists,
  explicit OIDC issuer/subject mappings, and additional group restrictions.
  Empty policy denies. Email-based mapping requires verified email.
- Enforce on ADE GET/POST, browser completion, account-driven and administrative
  profile issuance, and ACME finalization. Traverse DEP account pages, reject
  tombstones/wrong assignment, and propagate infrastructure failures.
- Use hashed random issuance grants with one-hour expiry, atomically bound to
  the first valid CSR. Same-CSR retries return the same certificate; another key
  is rejected. Validate CSR before reservation. Check issuance provenance during
  initial Authenticate using platform-correct identifiers.
- SCEP constructors require an explicit policy. Unauthenticated issuance is for
  test helpers; renewal-only endpoints verify an existing identity.
- Revocation defaults on: CRL TTL 24h, refresh 1h, OCSP TTL 15m. Register issuance
  durably and check unknown/revoked/expired identities before side effects.
- Validate CA/key agreement, authority, validity, and leaf lifetime bounded by
  issuer expiry. Persistent servers require persistent CA material.
- SCEP grants are bearer credentials before first use. Hardware identity needs
  supported ACME attestation plus admission policy.

### 3. Device/browser authentication and HTTP — S06, S08, S11

- Accept direct mTLS only with verified client chains. Forwarded certificates
  require roots and trusted socket peers; forwarded IP headers cannot confer
  trust. Reject duplicate/conflicting credential evidence. Roots do not disable
  mTLS. Protect the proxy backend connection and require header sanitization.
- Bind OIDC state to a per-flow random __Host- cookie (Secure, HttpOnly,
  SameSite=Lax), storing its digest and consuming only on a matching cookie.
  Keep five-minute lifetime, parallel flows, SQL-backed cross-replica state,
  and GET-only callbacks.
- Validate typed aud, matching azp when supplied/required for multiple audiences,
  and nbf. Refresh known JWKS keys after 15m; reject expired cache on fetch failure;
  throttle unknown-key refresh to once per minute.
- OIDC timeout 15s, HTTPS endpoints, and no credential-bearing redirects. Apply
  redirect guards to DEP, AxM, APNs, and webhooks. Authentication/profile responses
  use no-store/no-referrer. Logs exclude credentials and uncontrolled remote text.

Sources: [RFC 9440](https://www.rfc-editor.org/rfc/rfc9440.html),
[OAuth security](https://www.rfc-editor.org/rfc/rfc9700.html),
[OIDC validation](https://openid.net/specs/openid-connect-core-1_0.html#IDTokenValidation).

### 4. Persistence and protocol limits — S07, S09–S10

- Encrypt retained raw enrollment, command/result, and credential-bearing
  declaration/profile blobs across ordinary writes, replacement, import/export,
  and key rotation. Bind authenticated ciphertext to purpose and canonical row
  identity. Persistent reference storage requires a keyring; memory tests do not.
- Version the private DDM envelope: authenticate method/path/time/nonce/body and
  bind responses to request/status/content-type/body. Separate random keys >=32
  bytes, HTTPS except explicit loopback tests, freshness 5m, shared atomic replay
  records retained 10m, authenticated constructors, no redirects. Preserve Apple
  device-facing wire messages.
- Status defaults: depth 64, path 1024 bytes, 4096 items, existing body byte limit.
  Reject before writes. Include OTA/private DDM in configured route quotas.

Source: [OWASP cryptographic storage](https://cheatsheetseries.owasp.org/cheatsheets/Cryptographic_Storage_Cheat_Sheet.html).

### 5. Apple helpers and automation — A01, S12

- Add SCEP KeyIsExtractable/AllowAllAppsAccess pointer fields and round trips;
  reference profiles set false, retaining RSA-2048 minimum. Track capabilities
  and provenance; unknown supervision/ADE/user approval cannot satisfy command
  requirements. Preserve platform/channel account-driven token behavior.
- Scan both modules with GOWORK=off. Fail unsuppressed gosec findings, upload
  module-specific SARIF on failures, validate workflow edits, pin tools/actions,
  and scope job permissions. Exclude local lab credentials/state from Docker
  context and verify with harmless sentinels.
- Align root x/crypto with server v0.56.0. Module-only advisories are not reachable
  project vulnerabilities.

Sources: [Apple SCEP](https://developer.apple.com/documentation/devicemanagement/scep/payloadcontent-data.dictionary),
[Apple account-driven enrollment](https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment).

## Validation and acceptance

Audit baseline checks passed: targeted race-enabled library/server tests, SQLite
end-to-end tests, workspace and standalone-module govulncheck. No reachable or
imported-package vulnerabilities were reported; module-only advisories remain.

Required regressions cover:

- Channel/parent mismatches, scoped Cedar policy, collisions, and no mutations
  after rejection.
- Concurrent authentication, competing keys, transaction failure, retry
  idempotency, disable/poll/TokenUpdate/secret access, and parent lifecycle.
- Admission denial, verified account mapping, DEP tombstones/pages/failures,
  grant expiry/concurrency/key reuse, and ownership removed before finalize.
- Callback transfer between two browsers, absent/wrong cookies, parallel tabs,
  HEAD, replica completion, key rotation, malformed claims, TLS/proxy spoofing.
- Raw SQL secret sentinels, replacement/import, ciphertext swapping/rotation,
  credential redirects, private DDM replay/substitution/stale timestamps.
- Profile round trips, unknown capabilities, platform/channel behavior,
  deep/wide status limits, bounded allocations, and route quota coverage.

Run shared storage contracts on memory, SQLite, PostgreSQL and MySQL, including
independent-connection concurrency. Run repository lint, race tests, fuzz smoke,
end-to-end scenarios, coverage, and generation verification. Initialize Apple
schema at its pinned commit; never hand-edit generated outputs.

Hardware interoperability remains a separate acceptance check: applicable ADE,
account-driven, SCEP, ACME, DDM, renewal and revocation on supported Apple devices.
No physical devices or live PostgreSQL/MySQL were exercised in the audit phase.

## Documentation and completion

Update API docs, examples, threat model and operations with each change. Explain
admission, certificate transport, revocation, encryption coverage, secure startup
and explicit lab exceptions. Refresh API snapshots through repository tooling.
Add secret-free events for admission denial, identity mismatch, replay and
revocation failures. Resolve findings only after applicable regressions pass;
record actual validation results and any remaining hardware checks below.

## Implementation validation record

The implementation covers S01–S12 and A01. The findings above describe the audit
baseline; the current controls and their regression evidence are listed here.
The implementation and automated acceptance checks are complete. Physical Apple-device
interoperability remains a separate acceptance check.

| Findings | Implemented control | Regression evidence |
| --- | --- | --- |
| S01–S03 | Canonical channel/parent identity; atomic pin/reset/history; same-certificate retry preservation; disabled enrollment and parent gates; explicit identity replacement. | [Shared storage security contracts](../../storage/storagetest/security.go), [SQL concurrency and rollback](../../server/sqlstore/sqlite/security_test.go), [service boundaries](../../server/service/security_boundary_test.go). |
| S04–S05 | Explicit device/account admission, paginated DEP ownership checks, CSR-bound expiring SCEP grants, finalization/renewal readmission, issuance provenance and default revocation. | [Issuance tests](../../server/internal/app/issuance_security_test.go), [admission failure tests](../../server/internal/app/admission_boundary_test.go), [credential failure tests](../../server/internal/app/credential_fault_test.go). |
| S06 | Browser-bound, shared atomic OIDC state; audience/azp/nbf validation; bounded JWKS cache; HTTPS, timeout and redirect restrictions. | [Browser security](../../mdmprotocol/enroll/webauth/security_test.go), [provider security](../../mdmprotocol/enroll/webauth/provider_security_test.go). |
| S07 | Authenticated encryption for retained credential-bearing raw messages, commands/results and declaration blobs; row/purpose binding and rotation. | [Raw ciphertext and rotation](../../server/sqlstore/sqlite/security_test.go), [corruption and rollback](../../server/sqlstore/sqlite/security_failure_test.go), [DDM storage security](../../server/ddmstore/sqlstore/security_test.go). |
| S08 | Verified direct mTLS; trusted socket-peer certificate forwarding; duplicate/conflicting evidence rejection; protected backend startup checks. | [Certificate security](../../server/httpapi/cert_security_test.go), [runtime tests](../../server/internal/runtime/runtime_test.go). |
| S09 | HTTPS private DDM transport with independent keys, fresh nonces, shared replay prevention and request-bound response authentication. | [Envelope tests](../../server/ddmadapter/internal/proxywire/envelope_test.go), [split-deployment end-to-end tests](../../server/e2e/split_test.go). |
| S10 | Bounded status depth/path/item processing before writes; OTA and private DDM quota families. | [DDM limits](../../mdmprotocol/ddm/security_test.go), [reference security tests](../../server/internal/app/security_test.go). |
| S11 | Redirect rejection for credential-bearing OIDC, DEP, AxM, APNs and webhook clients; bounded reference OIDC requests. | [DEP redirect test](../../appleplatformservices/dep/redirect_test.go), [webhook redirect test](../../server/eventsink/redirect_test.go), provider tests above. |
| S12 | Both-module security scans, failing gosec findings, pinned tools/actions, scoped permissions, workflow-change triggers and verified Docker exclusions. | [Security workflow](../../.github/workflows/security.yml), [lint workflow](../../.github/workflows/go-lint.yml), [Docker context check](../../scripts/check-docker-context.sh). |
| A01 | Nonextractable, restricted-access reference SCEP keys; recorded tri-state command capabilities with provenance and conservative unknown values. | [Profile round trips](../../mdmprotocol/enroll/enroll_test.go), shared storage contracts, capability rollback tests above. |

Validation performed locally on the implemented code:

| Check | Result |
| --- | --- |
| Generation and exported API verification | Passed `make verify`; pinned Apple schema remains unchanged. |
| Executable scenario documentation | Passed `make bench-docs-check`. |
| Lint | Passed both modules, including the complete patch and newly created files, with automatic fixing disabled. |
| Workflow syntax | Passed `actionlint` for the changed lint, test and security workflows. |
| Unit/race tests | Passed both modules with shuffled test order. |
| Storage contracts | Passed memory, SQLite, PostgreSQL and MySQL, including concurrency, identity collisions and rollback. |
| Storage timing | Passed: PostgreSQL cleared 100,000 commands in 876.7 ms against the one-second local gate. |
| End-to-end scenarios | Passed SQLite scenarios and the TLS-protected split DDM fixture. |
| Built-process acceptance | Passed combined and split server topologies. |
| Fuzz smoke | Passed all 11 repository fuzz targets, 20 seconds per target. |
| Static security analysis | Both modules: zero gosec findings and zero Go analysis errors. |
| Dependency vulnerabilities | Both modules scanned independently with `GOWORK=off`; no reachable or imported-package vulnerabilities reported. A module-only advisory remains in unimported code. |
| Docker build context | Passed sentinel checks excluding local environment files, keys, certificates, databases, logs and lab state. |
| Coverage | Passed: **95.67% overall**, every nonexempt package at least 95%; app 95.13%, SQL common 95.04%. |

Coverage thresholds and exemptions are unchanged. The final report is generated
by `make coverage COVER_DIR=cover/security-coherent`; local profiles and the HTML
report are under `cover/security-coherent/`. Coverage uses matching source files,
including additional focused regression runs. Local checks do not constitute
an executed GitHub Actions run or physical-device certification. Hardware
acceptance remains: exercise applicable ADE, account-driven device/user channels,
SCEP and ACME enrollment/renewal, revocation, DDM synchronization and proxy
transport on supported Apple devices.

Operational configuration and retained limitations are documented in
[enrollment security](../operations/enrollment-security.md), the
[threat model](../security/threat-model.md) and
[decision 0050](../research/decisions/0050-enrollment-security-boundaries.md).
Persistent deployments require configured admission, persistent CA material,
storage keys and appropriate TLS trust. SCEP grants are bearer credentials
before first use; synchronized DEP inventory and stored IdP claims are the
reference admission inputs. Administrative exports remain privileged plaintext.
