# Enrollment security operations

Account-driven enrollment binds a reusable access token's Managed Apple
Account, subject and issuer to an issued identity certificate and enrollment.
Optional certificate status enforcement and inbound rate limits remain disabled
until configured. The design and supporting references are in
[decision 0047](../research/decisions/0047-enrollment-authentication-and-optional-security-services.md).

## Account-driven enrollment and migration

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

Legacy query-string enrollment credentials are rejected. Existing account-driven
profiles issued without a registered association need re-enrollment; importing a
certificate into the revocation registry does not create an account association.

With SQL storage, completed token grants, certificate associations, revocations
and quotas share the `protocol_state` schema, including its separate migrations.
Replicas must share that database, issuer keys and security configuration. The
reference browser OIDC handoff still uses an in-memory `webauth.StateStore`:
use session affinity for that handoff or inject a shared implementation when
embedding the library. The in-memory storage mode loses security state on restart
and is appropriate for development only.

## Optional certificate revocation

Enable publication and enforcement with explicit lifetimes, for example:

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

For an existing deployment, pause enrollment and device traffic during migration,
enable the registry on an isolated administration instance sharing the database,
and import existing leaf certificates before exposing enforcement to devices.
Retain every old CA certificate and signing key needed for status publication:

```sh
export DM_PKI_RETIRED_ISSUERS='[{"certificate":"/etc/dm/old-ca.pem","key":"/etc/dm/old-ca-key.pem"}]'
dmctl certificates import -file device.pem ISSUER
dmctl certificates status ISSUER HEX-SERIAL
dmctl certificates revoke -reason 1 ISSUER HEX-SERIAL
```

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
checks are exempt. An interval is the sustained spacing between accepted
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

## ADE policy and device validation

The reusable ADE hook remains available for deployments that require a DEP/ABM
ownership lookup. The reference server does not impose that policy universally;
configure a hook and its denial/audit behavior when ownership admission is part
of the deployment's threat model.

Automated tests cover protocol behavior, shared database concurrency and parsed,
signature-verified CRL/OCSP output. They cannot establish physical Apple device
compatibility. Before rollout, exercise the supported OS versions and enrollment
modes, including macOS device/user channels, token expiry during an interrupted
command response, certificate renewal and revocation, and proxy transport.
