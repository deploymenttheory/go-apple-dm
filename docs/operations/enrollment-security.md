# Enrollment security operations

Account-driven enrollment binds a reusable access token's Managed Apple
Account, subject and issuer to an issued identity certificate and enrollment.
Certificate revocation is enabled by default. Enrollment requires explicit
admission; inbound rate limits are configured separately. The design and supporting references are in
[decision 0047](../research/decisions/0047-enrollment-authentication-and-optional-security-services.md).

## Account-driven enrollment

The profile response creates a random enrollment reference. The reference server
uses it in the certificate subject and gives SCEP a profile-specific credential
bound to the first CSR. Only the trusted issuer callback can register the resulting
certificate. Presenting a certificate with that subject alone does not create an
association. Integrators using their own issuer must register issuance through
`Associations.RegisterCertificate` before returning the certificate.

The first authenticated `Authenticate` reserves the device-generated enrollment
identifier before the MDM store writes anything. The service confirms the
association after a successful write. A failed write leaves a reservation that
permits a retry with the same identifier; a different identifier remains denied.
This is a two-stage operation across separate stores, not a distributed transaction.
Subsequent requests require a confirmed association and matching identity.

Apple's macOS device channel omits the bearer token after enrollment; its user
channel sends it. Other supported account-driven channels send it. An invalid or
expired bearer on a known account-driven session returns the authentication
challenge so the client can authenticate again and retry the interrupted request.
Traditional enrollment does not acquire this reauthentication behavior.

Authorization codes and rotating refresh tokens are consumed atomically, after
validating their stored client, redirect and scope binding. Access tokens are
reusable until expiration or rotation. A custom verifier must return a stable
Managed Apple Account, subject and issuer and validate its own token signature,
issuer, audience and validity. Infrastructure failures must remain errors; only
the documented invalid-token errors initiate reauthentication. Apple-facing OAuth
does not require additional undocumented PKCE parameters. Upstream OIDC retains
its own protocol protections.

Query-string enrollment credentials are rejected. A certificate registry import
does not create an account association.

With SQL storage, token grants, browser handoffs, issuance grants, associations,
revocations, private-hop replay records and quotas use encrypted `protocol_state`.
Replicas share this database, issuer keys, encryption keys and policy. Browser
handoffs use per-flow `__Host-` cookies with Secure, HttpOnly and SameSite=Lax;
the stored cookie digest must match before GET callbacks consume state. HEAD
cannot consume a flow. Parallel tabs have separate cookies. OIDC validates
nonce, PKCE, issuer, typed audience, authorized party and validity. Known JWKS
keys expire after 15 minutes; a failed refresh cannot extend their trust. Unknown
key refreshes are limited to once per minute. HTTP requests have a 15-second
bound and cannot follow redirects carrying credentials.

## Enrollment admission and issuance

Set `DM_ENROLLMENT_POLICY_FILE` to a protected JSON file, for example:

```json
{
  "devices": [{"serial": "APPROVED-SERIAL", "udid": "APPROVED-UDID"}],
  "depAccounts": ["managed-dep-account"],
  "accounts": [{
    "issuer": "https://idp.example.com",
    "subject": "stable-subject",
    "managedAppleAccount": "person@managed.example.com",
    "groups": ["device-enrollers"]
  }]
}
```

Every supplied device identifier must match. DEP admission requires a device in
an explicitly listed account, an assigned or pushed profile matching that
account's configured profile, and no deletion tombstone. Synchronize inventory to keep that decision current. Account rules
require an exact issuer and subject or email; email rules require
`email_verified=true`. Configured groups require at least one membership.
An empty policy denies everyone. Administrative profile issuance also passes
admission. An embedded deployment may supply `EnrollConfig.Admission` with an
expiry-bearing grant; infrastructure failures must remain errors. Policy files
are loaded at startup; change the file and restart every replica to apply it.

Admission runs before profile delivery and again before SCEP issuance or ACME
finalization. A reference SCEP profile contains a random credential, stored only
as a hash, expiring within one hour and the admission grant lifetime. The first
valid CSR atomically reserves it; another key is rejected and the same CSR gets
the same certificate. Issued provenance is checked against Authenticate's
platform identifiers. Account-driven enrollment uses its registered account
association when hardware identifiers are unavailable. SCEP grants remain
bearer credentials before first use; hardware identity requires supported ACME
attestation plus organizational admission.

The library SCEP constructor requires an explicit challenge policy. `RenewalOnly`
rejects initial issuance. Reference profiles use RSA-2048 or stronger and set
`KeyIsExtractable=false` and `AllowAllAppsAccess=false` where the target supports those keys. The library builder applies these defaults to known Mac ACME and PKCS#12 targets and supported SCEP targets; explicit overrides remain available. Persistent enrollment
servers require `DM_ENROLL_CA_CERT_FILE` and `DM_ENROLL_CA_KEY_FILE`; the issuer
must be a valid CA with its matching private key. Leaf validity cannot exceed
issuer expiry. Only memory-backed development can use an ephemeral CA.

SCEP messages generated by the library use AES-128-CBC and SHA-256. Single-DES
envelopes are rejected. Library clients require verified HTTPS for automatic CA
discovery; HTTP callers must supply an independently trusted CA/RA bundle in
`EnrollOptions.Recipients`. `Roots` supplies issuer trust when the bundle contains
only RA certificates. Do not populate pins from an unauthenticated GetCACert
response. Clients reject redirects and validate response signer, transaction,
nonce, public key and certificate chain. An unset HTTP timeout becomes 30 seconds.

### macOS ACME credentials

The current Apple [ACME profile documentation](https://developer.apple.com/documentation/devicemanagement/acmecertificate)
and [declarative credential documentation](https://developer.apple.com/documentation/devicemanagement/acmecredential)
define separate availability and hardware requirements. macOS 13.1–13.x ACME
profiles require `HardwareBound=false` and explicit `Attest=false`. macOS 14+
supports hardware-bound EC keys on Apple silicon and T2; only Apple silicon
supports Mac attestation. DDM ACME credentials require macOS 14 or later. T2
credentials can request hardware binding with `Attest=false`. Other Intel Macs
use software keys.

`enroll.Profile.Target` and `MacHardware` validate known device context. The
reference server passes platform/version information from ADE, account-driven
requests and stored enrollments into profile composition. Administrative profile
requests accept `Product`, `OSVersion` and `MacHardware` (`apple-silicon`, `t2`,
`intel`). `ACMEConfig.MacHardware` resolves capabilities from trusted inventory;
it must return an error if that lookup fails. A tracked, authenticated
`DeviceInformation` response with `IsAppleSilicon=true` supplies the hardware
context for secondary credentials and replacements. This report selects the
payload; the ACME verifier still verifies hardware claims cryptographically.
`IsAppleSilicon=false` alone does not establish T2 capability.

Unknown Mac hardware is not inferred from the product string. Initial ACME
requests retain the explicitly configured key request when hardware is unknown;
operators must supply known capabilities for heterogeneous Mac fleets. Secondary
credentials with unknown hardware use software keys under the existing enrolled
identity authorization. Once Apple silicon is known, they request hardware
binding and attestation. Initial software-key ACME profiles are refused unless
`DM_ACME_ALLOW_UNATTESTED` explicitly permits that enrollment policy; SCEP remains
available. There is no optional Apple-conformance mode.

A declarative credential code is bound to the currently enrolled certificate,
expires after five minutes (or certificate expiry, whichever comes first), and
is claimed by one ACME order. The server rechecks the grant, enrollment,
current certificate and revocation state at challenge and finalization. Mac
secondary software/T2 credentials may use that scoped authorization with
`DM_ACME_ALLOW_UNATTESTED` disabled. Policies requiring fresh attested properties
still apply. Both the signed identifier and stored grant bind any requested
attestation; enabling general unattested issuance cannot downgrade that code.

Protect credential responses and their bearer identifiers. `Cache-Control:
no-store` covers profile and credential responses, including authentication and
handler errors. A Mac's MDM UDID remains separate from its attested
ProvisioningUDID. Initial ADE and replacement use the serial number as the
available hardware binding.

The pinned Apple YAML contains older Mac DDM restrictions and an ACME
extractability description that differ from Apple's live documentation.
Project-authored policy follows the live documentation; generated upstream
comments remain verbatim. See the [source comparison and validation record](../wip/apple-enterprise-hardening-2026-09-11.md).

### Library issuance and transport configuration

Library consumers can combine `scep.Grants.Verify` with
`scep.CertificateIssuer`. Configure a shared `state.Store`, current admission
callback, a pure `ca.Signer` that never re-enters the state transaction, and an
idempotent registration callback. Registration must establish the deployment's
certificate registry and enrollment associations before returning success.
A persistence or registration error returns no certificate. The persisted
receipt lets the next authorized retry complete registration with the same DER.
Use the same issuer and certificate policy on all replicas.

Existing reference `scep/grant/` and `scep/certificate/` keys and values remain
readable. Replacement records add `CSRHash` in their existing JSON state. A
legacy pending claim with only a public-key hash binds the next matching-key
request's CSR; an already recorded candidate certificate still cannot change.
Deploy the same version across replicas before relying on idempotent replacement
claims. Cancel and recreate pending replacement attempts during a mixed-version
rollout. New callers of `ReplacementChange{Op: "claim"}` must supply `CSRHash`.

AxM API/token URLs, DEP API URLs and APNs hosts require absolute HTTPS URLs with
no user information or fragment. Invalid endpoints are rejected before requests
send credentials, and URL errors omit the configured value. Custom transports
remain the embedding application's trust boundary. The HTTPS fixtures expose a
trusted client. Embedded reference servers can use `RootCAFile`; process-based
fixtures use `DM_AXM_ROOT_CA_FILE` and `DM_DEP_ROOT_CA_FILE`. These PEM bundles
replace the client's trust roots, preserve certificate/hostname verification,
and cannot be combined with an injected HTTP client.

## Certificate revocation

Publication and enforcement are enabled by default, with these lifetimes:

```sh
export DM_PKI_REVOCATION=true
export DM_PKI_CRL_TTL=24h
export DM_PKI_CRL_REFRESH=1h
export DM_PKI_OCSP_TTL=15m
```

Enrollment and an operator-managed persistent CA must already be configured.
New certificates carry CRL and OCSP URLs. The registry must persist issuance
before a certificate is returned. Unknown, expired or revoked certificates fail
the status gate for CMS, mTLS and trusted-proxy certificate transports regardless
of pin mode. Status checking supplements certificate chain verification. SCEP
renewal cannot bypass a failed status check with a shared challenge password.

Retain every retired issuer certificate and signing key needed for status
publication. Administrative operations include:

```sh
export DM_PKI_RETIRED_ISSUERS='[{"certificate":"/etc/dm/old-ca.pem","key":"/etc/dm/old-ca-key.pem"}]'
dmctl certificates status ISSUER HEX-SERIAL
dmctl certificates revoke -reason 1 ISSUER HEX-SERIAL
```

`DM_PKI_REVOCATION=false` explicitly disables publication and status enforcement;
certificate chain validation, admission and enrollment pinning remain required.

`ISSUER` is the SHA-256 fingerprint of the issuer certificate, in the same format
as registry records; serials are hexadecimal. Import accepts PEM or DER. Re-import
is idempotent and cannot undo revocation or replace issuance provenance. Import,
read and revoke have separate administration policy actions. Revocation events
contain issuer, serial and reason; normal administration auditing records the
actor and action. Device checkout does not automatically revoke a certificate.

Supported irreversible RFC 5280 reason codes are 0, 1, 2, 3, 4, 5, 9 and 10.
Certificate hold and remove-from-CRL are intentionally unsupported. The optional
ACME `revokeCert` endpoint accepts the certificate key, the issuing account, or
an account with current authorizations for every identifier in issuance provenance.

CRL numbers and signed DER commit together under an issuer lock. Replicas read
shared publications and a revocation marks the publication for regeneration.
OCSP answers unknown for an unregistered serial. Keep publication endpoints
reachable, monitor signing and database failures, and choose lifetimes to fit
the deployment's required status freshness. Keep retired issuer keys protected
until their certificates no longer require status responses.

## Optional inbound quotas

Configure only the route families that need admission control. Each configured
family requires a per-peer quota and an aggregate quota. This example is a syntax
example, not a production sizing recommendation:

```sh
export DM_RATE_LIMITS='{"auth":{"interval":"1s","burst":10,"global_interval":"10ms","global_burst":100}}'
export DM_RATE_LIMIT_MAX_ENTRIES=4096
```

Families are `enroll`, `auth`, `scep`, `acme`, `admin`, `pki` and `mdm`. Health
checks are exempt. `enroll` includes OTA routes, and `mdm` includes the private
DDM ingress. An interval is the sustained spacing between accepted
requests, with a burst allowance; both quota buckets commit or neither does.
Intervals must be whole microseconds. SQL accounting samples database time after
locking, so replica clock differences cannot create extra quota. State keys are
hashed and capacity is bounded (default 4096, maximum 10000).

Capacity accounting serializes writers in the reference limiter namespace. Size
and load-test the deployment accordingly; library consumers can separate
independent workloads into distinct namespaces. Exhausted quotas return 429 and
`Retry-After`; database or capacity failures return 503. ACME failures use ACME
problem JSON. These controls do not replace network-level denial-of-service
protection or application-specific admission policy.

The peer address comes from the socket by default. Set `DM_TRUSTED_PROXIES` to a
comma-separated list of explicitly trusted CIDRs only when needed. Forwarded
addresses are walked from the trusted peer toward the first untrusted hop;
malformed headers fall back to the socket address.

## Identity, transport and storage

Raw enrollment IDs have one immutable channel and parent. Storage rejects
mismatches, including pending user-authentication reservations. Administrative
Cedar authorization resolves the stored identity first. Authenticate commits
reset, certificate history and pinning atomically; the same certificate retry
preserves queues and escrow. Both the reusable service and reference server deny
changed-certificate reenrollment by default. Explicit replacement authorizes a
change. Unpinned records cannot acquire a pin through polling. Disabled records
cannot reactivate via TokenUpdate, obtain queued commands or access device
secrets, DDM or assets; a disabled parent gates its user channels.

Direct mTLS requires verified client chains. CMS and certificate evidence must
agree when more than one source is supplied. `DM_CERT_HEADER` requires
`DM_CA_FILE` and explicit `DM_TRUSTED_PROXIES` CIDRs. Trust is based on the actual
socket peer, never an asserted forwarding header. The proxy must verify client
certificate possession, remove inbound certificate headers and set exactly one
validated value. Protect its backend with TLS or a loopback connection; the
runtime rejects a plaintext header backend on a non-loopback listener.

Persistent stores require `DM_STORAGE_KEYS`. Encryption covers raw Authenticate,
TokenUpdate, UserAuthenticate, commands, results and error chains, existing
escrow and private-key columns, protocol state, and declaration/version/snapshot
blobs. Ciphertext is authenticated against its purpose and row identity. Rewrap
all applicable MDM, DDM and protocol-state stores when rotating keys. Exports are
plaintext privileged material; protect them and backups separately. Database
metadata and status/audit records are not whole-database encrypted.

The private DDM connection requires HTTPS and two independent random keys of at
least 32 bytes. Set `DM_DDM_ROOT_CA_FILE` for private server trust and configure
`DM_TLS_CERT_FILE`/`DM_TLS_KEY_FILE` on the DDM role. Requests authenticate method,
request target, content type, timestamp, nonce and body; responses bind to that
request, status, content type and body. Five-minute freshness and shared atomic
nonce records retained ten minutes reject replay across replicas. Both adapters
refuse redirects. `AllowInsecureForTests` is a programmatic literal-loopback test
exception; there is no environment switch. `scripts/testdb.sh ddm-up` exercises
TLS with a generated test CA. When native TLS is enabled, replace the image's
HTTP healthcheck with a probe that trusts the configured CA.

Command eligibility uses recorded device capability observations. Unknown
supervision, ADE or user approval cannot satisfy a command requirement. Query
DeviceInformation and SecurityInfo before enqueueing commands requiring those
capabilities. DDM status processing rejects excessive nesting (64), key paths
(1024 bytes) or JSON item count (4096) before storage writes, in addition to body
byte limits.

Metadata-only security events identify admission denial, identity rejection,
certificate status rejection and private-hop rejection. Enable an event sink or
persistent audit to retain them. Events contain no credential or remote error
text. DEP, AxM, APNs and webhook clients reject redirects; use trusted HTTPS
endpoints for production outbound credentials.

## Device validation

Automated tests cover protocol behavior, shared database concurrency and parsed,
signature-verified CRL/OCSP output. Before rollout, exercise supported Apple OS
versions and enrollment modes on physical devices, including ADE, account-driven
macOS device/user channels, token expiry during an interrupted response,
SCEP/ACME renewal and revocation, DDM synchronization and proxy transport.
