# Apple-conformant security hardening — 2026-09-11

This record covers the library and reference server changes made from
`b324c88`. Findings distinguish reproducible defects from deployment controls.
Documentation was checked on 2026-09-11 against Apple's live developer pages
and the repository's pinned Apple schema at
`67045e2fa06f528b196c01edee6a8bf88b844beb`. The schema pin and generated Apple
definitions are unchanged. Automated tests establish the behavior exercised by
the tests; they do not constitute Apple certification or physical-device results.

## Source reconciliation

| Source | Relevant expectation | Consequence for this change |
|---|---|---|
| [Apple: ACME certificate](https://developer.apple.com/documentation/devicemanagement/acmecertificate) | The client orders a permanent identifier, answers `device-attest-01`, and submits a CSR for its generated key. Attestation freshness derives from the challenge token. | Preserve those exchanges and cryptographic checks, including on receipt recovery. Hardware-dependent attestation remains supported. |
| [Pinned Apple ACME payload, DirectoryURL and ClientIdentifier](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/mdm/profiles/com.apple.security.acme.yaml#L47) | The directory uses HTTPS. The server may use the client identifier to prevent multiple certificate issuances. | HTTPS is an Apple requirement. The existing single-use identifier policy is Apple-permitted; serializing it is our implementation responsibility. Apple does not prescribe database locks or receipts. |
| [Apple: managing device-management certificates](https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices) | Devices require trusted service certificates and distinct identity certificates; the service checks the certificate's association with the reported UDID. Apple describes CMS identity through proxies and profile replacement before certificate expiry. | Preserve identity association, proxy/CMS transport and authorized replacement. Requiring a client certificate on every enrollment request would interfere with preidentity flows, so the shared listener retains optional verified client certificates. |
| [RFC 8555, issuance](https://www.rfc-editor.org/rfc/rfc8555#section-7.4) | Clients can correct a refused CSR while the order is ready; they poll processing orders and download certificates from valid orders. | Retain corrected-CSR retries before a receipt exists. Processing order polls can complete registration after a restart. A committed receipt binds the original CSR and DER. |
| [Apple: ACME credential](https://developer.apple.com/documentation/devicemanagement/acmecredential) | Declarative credentials use the ACME exchange with their own OS and hardware availability. | This change adds no blanket attestation requirement and does not conflate profile and declarative-credential availability. |
| [Apple: simple account-driven authentication](https://developer.apple.com/documentation/devicemanagement/implementing-the-simple-authentication-account-driven-enrollment-flow) | Apple's account-driven client has a defined discovery and authentication exchange. | No new client fields, authentication round trips or bearer-token rules are introduced by admin/transport hardening. Existing channel-specific behavior remains covered by regressions. |

The live Apple ACME documentation and pinned YAML have known differences around
Mac capabilities and extractability. The earlier [source comparison](apple-enterprise-hardening-2026-09-11.md)
continues to apply; this work does not restore older generated policy assumptions.

## Findings and dispositions

Severity describes this reference composition and the stated prerequisites; it
is not a CVSS score. All six findings below are addressed in code.

### ACME-1 — concurrent finalization and stale state (high, authorized order required)

Before the change, two signed finalize requests with distinct nonces could both
observe a ready order, pass policy, and sign different certificates. The new
`TestConcurrentFinalizeIssuesOneCertificate` was run against the original logic
and failed with two signing calls. A delayed challenge result could also write
an obsolete state after another request had completed issuance.

[`UpdateOrder`](../../pki/acme/store.go) now locks an existing order before
reads. Challenge settlement and finalization re-read current state. SQL acquires
a write lock before snapshot reads, including on MySQL; memory stores serialize
under their transaction lock. The protocol never updates existing order state
through an unlocked read-modify-write sequence.

**Classification:** Apple-permitted issuance policy, reconciled with the pinned
`ClientIdentifier` description and the live ACME key-binding exchange above.
Scope is every supported Apple ACME client; no payload availability changed.
Evidence: [race/stale-result tests](../../pki/acme/issuance_security_test.go),
[shared order transaction contract](../../storage/acme/acmetest/order_security.go),
[SQL locking](../../server/acmestore/sqlstore/tx.go).

### ACME-2 — durable issuance recovery (high integrity impact, storage/registration failure)

The previous reference signer could persist a certificate before the final
order state committed. A subsequent failure left retries able to sign again.
The signer now performs only signing inside the order transaction. It commits
one certificate receipt and CSR hash before registration side effects. Both
finalize retries and standard POST-as-GET order polls can complete registration
with the same DER. Pending receipts cannot be downloaded and are not advertised
as certificate URLs. Admission, attested key and deadlines are checked again.

**Classification:** project integrity control implementing the Apple-permitted
issuance policy, with RFC 8555 retry semantics. It applies to attested and
explicitly authorized unattested orders. The receipt does not make an external
registration callback transactional: callbacks must be idempotent and safe for
concurrent calls, and pure signers must not expose uncommitted certificates.
Evidence: [issuance sequence](../../pki/acme/issuance.go),
[failure/restart recovery tests](../../pki/acme/issuance_security_test.go),
[reference composition](../../server/internal/app/acme.go).

### ADMIN-1 — delegated credential authority (high, scoped administrator required)

An operator permitted to manage principals could create an absent name already
granted broader authority directly by Cedar. The pre-change reproduction created
`reserved` under a direct permit policy and returned its token. Role-subset
checks cannot prove authority containment with named principals, context and
forbid rules.

Principal creation/update, rotation (including self-rotation), revocation and
deletion now require root outside Cedar. Stored root principals still require
the relevant policy permit; the configured break-glass token remains a deliberate
policy bypass. Policy-authorized reads and scoped device actions remain usable.

**Classification:** infrastructure control on the project's administrative API.
Apple's device exchanges in the sources above do not depend on this API or on
delegated operator tokens. This is not claimed as an Apple root-administration
requirement. Evidence: [manager](../../server/adminauth/manager.go),
[original bypass regression](../../server/adminauth/issuance_security_test.go),
[HTTP enforcement test](../../server/internal/app/adminprincipals_test.go).

### ADMIN-2 — active-root lockout (medium availability impact, root mutation required)

The previous guard counted revoked/expired roots and separated the count from
the write. A pre-change test revoked a root and then successfully revoked the
last active root. Concurrent root removal had the same count/write gap.

`Store.ApplyPrincipal` serializes lifecycle changes and checks for a surviving
active root in the same transaction. The SQL implementation uses the existing
policy-version singleton as a lock without changing policy version. The guard
covers delete, revoke, demote and immediate expiry through rotation. It cannot
prevent later natural expiry or an administrator writing a policy that denies
every root; those remain operator responsibilities.

**Classification:** infrastructure availability control; no Apple wire behavior
or device lifecycle changes. Evidence: [atomic mutation](../../server/adminauth/sqlstore/change.go),
[concurrent backend contract](../../server/adminauth/adminauthtest/security.go),
[revoked-root regression](../../server/adminauth/issuance_security_test.go).

### TLS-1 — insecure endpoints and remote listener defaults (high if network-exposed)

The reference process defaulted to a wildcard plaintext listener; the CLI could
send bearer credentials over remote HTTP or disable certificate verification.
The library ACME constructor also accepted an HTTP public base URL.

The binary and container now default to `127.0.0.1:8080`. A non-loopback listener
requires native TLS. A reverse proxy may use a literal-loopback HTTP backend or
a remote TLS backend. The CLI requires verified HTTPS remotely, accepts local
HTTP only on literal loopback IPs, and rejects `-insecure`. ACME's public base URL
requires HTTPS without user information, query or fragment.

**Classification:** the ACME HTTPS check is an Apple requirement. Restrictions
on remote internal listeners, operator URLs and ambiguous URL components are
project transport policy, compatible with Apple's trusted public service and
CMS proxy paths. Evidence: [ACME configuration](../../pki/acme/server_test.go),
[listener tests](../../server/internal/runtime/runtime_test.go),
[CLI trust tests](../../server/internal/dmctl/adminclient/adminclient_test.go).

### SINK-1 — webhook transport and URL disclosure (medium, configured sink required)

HTTP webhooks exposed projected device data to the network. URL-bearing client
errors could disclose receiver secrets embedded in a URL path or query.
Webhooks now require HTTPS, support `DM_WEBHOOK_ROOT_CA_FILE`, preserve TLS
verification, refuse redirects, and return sanitized transport error strings.
Wrapped causes remain inspectable by trusted code and must not be logged raw.

**Classification:** infrastructure control. The webhook is a project event sink;
Apple's device protocol does not require plaintext webhook delivery. No new
device fields or Apple service dependencies are introduced. Evidence:
[sink implementation](../../server/eventsink/webhook.go),
[secret and URL regressions](../../server/eventsink/security_test.go),
[private-CA delivery tests](../../server/internal/app/sinks_test.go).

## Wider boundary review

These areas were checked alongside the new findings. No additional change was
selected without evidence of a defect and a compatible remedy.

| Boundary | Existing controls retained and review limits |
|---|---|
| [MDM identity and decoding](../../server/httpapi/cert.go), [service](../../server/service/) | Verified chains, agreement between simultaneous identity sources, trusted socket-peer CIDRs, immutable enrollment/channel association and bounded decoding. Neither arbitrary certificate headers nor a matching subject alone establish enrollment. |
| [Enrollment](../../mdmprotocol/enroll/), [SCEP](../../pki/scep/) | Explicit admission, shared issuance grants, CSR-bound retry receipts, account associations, no-store credential responses and status checks. Bearer-token reuse and macOS channel behavior remain intentional. |
| [Browser authentication](../../mdmprotocol/enroll/webauth/) | Browser-bound state, OIDC nonce/PKCE/issuer/audience checks, expiring JWKS trust and bounded outbound requests. Apple's client is not required to add undocumented OAuth parameters. |
| [DDM engine](../../mdmprotocol/ddm/), [private adapters](../../server/ddmadapter/) | Enrollment snapshots, input bounds, lifecycle cleanup, HTTPS, two directional keys and atomic replay claims. A blanket new per-device request quota was not imposed. |
| [Apple clients](../../appleplatformservices/) | Existing HTTPS validation, credential-bearing redirect refusal, bounded requests and service-specific retry/error handling. Custom transports remain an integrator trust boundary. |
| [Encrypted storage](../../storage/crypt/), [protocol state](../../server/statestore/) | AES-GCM with purpose/row binding, explicit persistent keys, rewrap guards and shared state. Metadata, status, audit records and privileged plaintext exports are not whole-database encrypted. |
| [Revocation](../../pki/revocation/), [quotas](../../ratelimit/) | Registry checks independent of pinning, serialized CRL publication, unknown-status handling, bounded atomic peer/global quotas. Checkout is not treated as mandatory certificate revocation. |
| [CI](../../.github/workflows/), dependencies | Workflow pinning and security checks reviewed; vulnerability and static-analysis results are reported separately from runtime regressions. A scanner cannot prove the absence of vulnerabilities. |

## Library and deployment changes

Follow the [operations guide](../operations/enrollment-security.md) for the
complete sequence and limitations. In particular:

1. Move side effects from ACME signers to the idempotent registration callback.
2. Implement and contract-test `acme.Store.UpdateOrder` and
   `adminauth.Store.ApplyPrincipal` in custom backends.
3. Stop old writers before switching replicas; retain common issuer keys,
   policy, shared database and encryption material. Completed records remain
   readable without new SQL migrations.
4. Configure native TLS for remote/container-network listeners. Update probes
   to trust their server CA. Keep the public ACME URL HTTPS behind proxies.
5. Migrate `dmctl` contexts to HTTPS and `-ca-file`; local HTTP examples use
   `127.0.0.1`. Arrange root-operated credential rotation for scoped principals.
6. Migrate webhook receivers to HTTPS and configure private CA trust if needed.
   Retain monitoring for failed registration, webhook delivery and token expiry.

## Automated validation

Validated on macOS with Go 1.27.1. PostgreSQL and MySQL used temporary local
Docker databases, removed after validation. No dependency or schema pin changed.

| Check | Final result |
|---|---|
| `make test` | Pass for both modules with the race detector and shuffled tests. |
| `make test-storage` with both database DSNs configured | Pass, including memory, SQLite, PostgreSQL and MySQL contracts, concurrent order updates, active-root protection and rollback checks. |
| `make test-e2e`, with `E2E_STORE=sqlite`, `inmem` and `postgres` | All three runs pass, including the shared simulated acceptance scenarios. |
| `make test-acceptance` | Pass against built server/CLI processes, including native split topology. |
| Focused ACME/admin regressions with `-race` | Pass: concurrent finalize, delayed challenge, restart, order-poll recovery, independent ACME client retry, failed transaction reads/writes, invalid receipt, policy withdrawal, expiry, credential delegation and root lifecycle. |
| `make verify` | Pass; pinned Apple schema regeneration and workflow checks remain clean. |
| `make lint` and explicit complete-patch lint | Pass in both modules. The extra patch includes untracked Go files so new implementations are included despite the repository's diff-based lint configuration. |
| Standalone `gosec` | Zero findings and no package-loading errors: 304 library files and 166 server files analyzed. |
| `GOWORK=off govulncheck -show verbose ./...` in each module | Zero reachable vulnerabilities and zero findings in imported packages. Both scans report one module-only advisory, [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932), concerning unimported `x/crypto/openpgp`. |
| `make fuzz-smoke` | All 11 targets pass, 20 seconds each. |
| `make coverage` | Pass: **95.62% overall**, every non-exempt package meets 95%; ACME is 95.25%. No threshold or exemption changed. |
| `make bench-docs-check`, changed Markdown link checks, `git diff --check` | Pass. |

Initial sandbox attempts could not bind test sockets or read build-cache/submodule
files; these were rerun with the required access. Concurrent broad suites also
exhausted the ten-minute timeout while SQLite's existing `TestClearBatches`
seeded its queue. It passed in isolation, and subsequent complete unit/storage
runs passed without increasing their default timeout. These initial failures
are not counted as successful validation.

Local run logs use `/tmp/apple-posture-*`; merged coverage is in
`cover/merged.html` and `cover/packages.txt`. These are local validation outputs,
not committed fixtures. The README, operations guides, relevant architecture
decisions, package/API comments and threat model have been reviewed against the
final defaults, issuance sequence and integration responsibilities. Earlier
audit records now link to this subsequent change.

## Physical-device checklist — not executed

Record model/SoC, OS build, enrollment mode/channel, profile revision, transport,
server revision and timestamp for each result. Use authorized lab devices and
retain redacted protocol observations; no live enrollment, revocation, replacement
or erase operation was performed during this work.

| Exercise | Expected result and evidence to retain |
|---|---|
| ACME profile on supported hardware, and DDM ACME credential separately | Enrollment/credential installation completes with the configured key and a valid chain. Check the actual platform availability in the live Apple pages above; profile support alone is not DDM credential support. |
| Apple silicon Mac; T2 Mac; software-key Intel Mac where supported | Follow the existing [Mac capability matrix](../operations/enrollment-security.md#macos-acme-credentials). Confirm unattested policy paths work only when authorized and cannot downgrade an explicitly attested binding. |
| Registration outage and server restart during ACME finalization | Standard order polling recovers the same receipt and certificate serial/DER after service restoration. No second issued certificate or premature download appears. |
| Same-order retries and malformed/corrected CSR in a protocol harness | One completed certificate; a corrected CSR works before a receipt exists; a different CSR cannot replace a committed receipt. Physical-client behavior supplements the automated harness. |
| Direct TLS identity and CMS through a controlled TLS proxy | Both supported paths succeed with valid enrolled identity. Untrusted proxy evidence, mismatched identities and untrusted public HTTPS fail. Initial enrollment still reaches preidentity routes. |
| ADE and account-driven enrollment, macOS device/user channels | Authenticate/TokenUpdate complete. Token expiry triggers the documented reauthentication path on applicable channels; the macOS device-channel bearer exception remains intact. |
| Authorized profile replacement before certificate expiry | Device installs the replacement, uses the new identity and continues polling. Follow Apple's profile/certificate renewal guidance; do not wait for an expired trust chain. |
| DDM synchronization across native and split topology | Declarations, assets and status round trips complete; checkout clears associated state. Retain request/status evidence without credential bodies. |
| Revoked/expired identity and administrative transport | Device identity rejection matches the configured status policy. Remote CLI uses trusted HTTPS; failed TLS prevents bearer transmission. Root credential rotation preserves usable administration. |

The hardware matrix is an operator validation prerequisite, not a claim that
every Apple platform was exercised by the simulator.
