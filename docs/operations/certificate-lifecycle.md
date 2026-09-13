# Certificate setup and renewal

`devicemanagement/pki/lifecycle` manages certificate requests, private keys,
returned certificates, activation, and renewal using persistent state. The
reference server uses encrypted SQL storage and exposes the same workflows through
`dmctl setup` and `/admin/v1/setup`. Local bootstrap requires access to the setup
file and its secret references; remote operations require administrative authorization.

The annual Apple MDM push identity belongs on the server. Devices retain its APNs
topic. Renew the existing certificate in Apple's portal using the original Apple
account; the library refuses a renewal that changes the topic. Offline devices do
not prevent APNs activation. Device identity renewal and enrollment CA rollover
have their own tracked process. See Apple's [customer push setup documentation](https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers)
and [MDM payload documentation](https://developer.apple.com/documentation/devicemanagement/mdm).

## Bootstrap

Build the two commands from the repository root:

```sh
go build -o test-lab/local/certs/bin/dmctl ./server/cmd/dmctl
go build -o test-lab/local/certs/bin/dmserver ./server/cmd/dmserver
```

Choose `customer`, `vendor`, or `combined`. A vendor deployment holds the Apple
vendor signing identity and signs public customer CSRs. A customer deployment
holds its own push key and device enrollment state. `combined` performs both roles
for a local lab or a deployment that operates its own vendor identity.

```sh
dmctl setup init -dir test-lab/local/certs/managed -role combined \
  -public-url https://localhost:8443 -listen 127.0.0.1:8443
export DM_SETUP_FILE="$PWD/test-lab/local/certs/managed/setup.json"
```

Initialization creates `setup.json`, protected secret files, and a SQLite database.
It resumes an existing bootstrap without replacing its keys. Directories use mode
0700 and files use 0600. Files are synced before atomic publication. The setup file
contains configuration and secret references; SQL DSNs containing credentials are
stored in protected files. Keep the database **and** the external encryption keys
in a recoverable backup. The key ID is part of the encryption identity and must
be retained with its material.

For PostgreSQL or MySQL, supply `-storage postgres` or `-storage mysql` and put the
DSN in `DM_DSN` (or name another environment variable with `-dsn-env`). Explicit
`DM_*` environment settings override file settings. Managed identities conflict
with static TLS, enrollment CA, or file-based push identities; remove those old
settings after adoption.

## HTTPS and the enrollment issuer

A local lab can create its HTTPS identity and a separate HTTPS trust CA:

```sh
dmctl setup https lab -cn 'Local MDM HTTPS' -hosts localhost,127.0.0.1,::1
dmctl setup https activate -revision 1
dmctl setup profile trust -out test-lab/local/certs/profiles/managed-https-trust.mobileconfig
dmctl setup issuer create -cn 'MDM enrollment CA'
dmctl setup issuer activate -revision 1
```

New lab CAs default to ten years. The HTTPS leaf uses a distinct private key and
is renewed automatically under the same lab CA. Install the HTTPS trust profile
on lab devices before connecting them. A public CA-issued HTTPS identity normally
needs no custom HTTPS trust profile.

For public HTTP-01 issuance, configure `setup.http01Listen` in `setup.json` (or
supply `-http01-listen :80` at initialization). Configure public DNS and route port
80 to the challenge listener. Then run:

```sh
dmctl setup https acme -cn mdm.example.com -hosts mdm.example.com \
  -contact mdm-operations@example.com -accept-terms -http01-listen :80
```

The local command serves HTTP-01 while completing issuance and activation. Once
running, the server handles subsequent orders. All replicas can answer challenges
from shared state; one lease holder advances an order. Account keys, account/order
URLs, challenges, retry deadlines, and failures persist across restarts. A lost
account registration response reuses the original account key. HTTP-01 supports
explicit DNS names; wildcard and IP identifiers are rejected. Public port 80 is
required by [Let's Encrypt's HTTP-01 validation](https://letsencrypt.org/docs/challenge-types/).
The default directory is production; `-directory` can select a staging CA.

Alternatively, create the HTTPS CSR, submit it to your chosen CA, and import the
returned chain:

```sh
dmctl setup https request -cn mdm.example.com -hosts mdm.example.com
dmctl setup workflow export -id https -artifact csr -out https.csr.pem
dmctl setup https import -revision 1 -cert https-chain.pem
dmctl setup https activate -revision 1
```

HTTPS imports must match the pending key, requested hostnames, validity period,
and trusted chain. An existing HTTPS identity can instead be adopted with
`setup adopt -kind https -id https -cert chain.pem -key key.pem`.

## Apple vendor signing identity

Run these commands on the vendor deployment, or in the combined lab:

```sh
dmctl setup vendor request -cn 'Example MDM Vendor' -organization 'Example Ltd'
dmctl setup workflow export -id vendor -artifact csr -out vendor.certSigningRequest
```

**Operator step:** In the [Apple Developer certificate portal](https://developer.apple.com/account/resources/certificates/list),
create an **MDM Vendor CSR Signing Certificate**, upload this CSR, and download
the certificate. This requires the applicable Apple developer account access.
Follow Apple's [vendor certificate instructions](https://developer.apple.com/help/account/certificates/mdm-vendor-csr-signing-certificate/).

```sh
dmctl setup vendor import -revision 1 -cert vendor.cer
dmctl setup vendor activate -revision 1
```

The importer accepts DER or PEM and completes Apple's issuer chain from the
bundled public authorities. It verifies the key, validity, signing purpose,
ordered chain, and trust anchor. The legacy Apple root's SHA-1 self-signature is
not revalidated as an issued certificate; signatures of issued chain members
still undergo normal verification.

## Customer push identity

Run on the customer deployment or combined lab:

```sh
dmctl setup push request -cn 'Example MDM Customer' -organization 'Example Ltd' \
  -account mdm-owner@example.com
dmctl setup push sign -revision 1
```

In a combined deployment, signing uses the active local vendor identity. A
customer can configure `setup.vendorUrl` and `setup.vendorTokenFile` to call a
vendor's HTTPS setup API. Give that credential permission for `signCustomerPushRequests` (signing only).
Only the public CSR crosses that boundary; the customer private key remains in
customer storage. Redirects cannot forward the signing credential.

For an offline exchange between separate deployments:

```sh
# Customer deployment
dmctl setup workflow export -id push -artifact csr -out customer.csr.pem

# Vendor deployment, using its own setup file
dmctl setup vendor sign -csr customer.csr.pem -out customer.signed.csr

# Customer deployment
dmctl setup push sign -revision 1 -signed-request customer.signed.csr
```

The customer verifies the returned signature, vendor chain, and exact CSR before
saving the portal artifact.

```sh
dmctl setup workflow export -id push -artifact signed-request -out customer.signed.csr
```

**Operator step:** Upload the signed request to the [Apple Push Certificates Portal](https://identity.apple.com/pushcert/)
and download the MDM push certificate. Record which Apple account owns it.

```sh
dmctl setup push import -revision 1 -cert downloaded-mdm-push.pem
dmctl setup push activate -revision 1
dmctl setup check
dmserver --setup-file "$DM_SETUP_FILE"
```

The active push certificate determines the enrollment topic. If a setup API server
was already running before the initial push and issuer activation, restart it once
to enable enrollment routes. Subsequent push, HTTPS, and issuer transitions are
read from storage without replacing the process.

Profile export uses the existing enrollment admission and hardware policy:

```sh
dmctl setup profile export -device-id DEVICE-UUID -serial SERIAL \
  -product Mac16,12 -os-version 26.6.2 -hardware apple-silicon \
  -identity acme -access-rights 19 -out device-enrollment.mobileconfig
```

Configure enrollment admission before export. ACME device attestation retains the
server's existing policy; enabling certificate setup does not permit unattested
enrollment. Install the profile on the device using the normal enrollment flow.

## Annual renewal and operator status

```sh
dmctl setup status
dmctl setup check
dmctl setup workflow show -id push
dmctl setup workflow history -id push
dmctl setup push renew
```

`check` exits unsuccessfully if required certificates are missing or expired. A
local check also validates the complete server configuration without starting its
workers or listeners. `status` reports certificate readiness separately from
whether enrollment is enabled in the current process.

Use `renew -force` to prepare an explicit early renewal; retries reuse the pending
revision. Without `-force`, a completed renewal remains unchanged until its next
renewal window.

The worker prepares APNs/vendor renewal 60 days before expiry (or one third of a
shorter certificate's lifetime). It keeps the active identity while creating one
pending key and CSR. It records persistent notices at the renewal threshold and
30, 14, 7, 1, and 0 days. Combined/customer signing integration prepares the portal
artifact automatically; Apple's portal steps remain operator actions. Use the
pending revision shown by status in all subsequent commands.

For an annual push renewal, select **Renew** on the existing portal certificate
using the owning Apple account, upload the new signed request, then import and
activate the result. The new certificate must extend validity and retain the exact
APNs topic. A failed import or activation leaves the previous identity active.
Activation updates the encrypted workflow and runtime push row in one SQL
transaction. The runtime observes its version and reloads the APNs identity.
HTTPS is loaded for new TLS handshakes; existing connections can finish normally.

`workflow cancel -id ID -revision N` cancels only a pending revision. Public
exports support `csr`, `signed-request`, and `certificate`; private-key export is
not an administrative operation. `workflow history` contains public fingerprints,
phases, time, and actor attribution. Pagination uses `-cursor` and `-page-size`.

## Device identities and enrollment CA rollover

Device identities nearing expiry use the authenticated enrollment replacement
handshake. The server retains the original management endpoint, topic, profile
identity and access rights. It tracks issuance, candidate authentication,
TokenUpdate, and command acknowledgement before committing the new pin. Expiry of
the short-lived issuance grant does not discard confirmation for an already
issued candidate; no new key or CSR can claim the expired grant.

Locally managed enrollment roots prepare a successor before expiry. External
issuers require a returned certificate from their CA. To prepare manually:

```sh
dmctl setup issuer renew -force
# For a locally managed root; for an external issuer, export its CSR and import the chain.
dmctl setup issuer create -revision 2
dmctl setup issuer rollover -revision 2
dmctl setup issuer status -revision 2
```

Rollover installs issuer-specific SCEP and device ACME routes, trusts both CAs,
switches the default for new profiles, and tracks existing devices separately.
Each device first acknowledges an overlapping trust profile, then receives its
replacement identity. Offline devices remain in persistent migration state.
Unsupported profile installation, missing original profile metadata, failed
commands, or unknown issuer evidence produce an explicit blocker.

```sh
dmctl setup issuer retry -revision 2 -device-id DEVICE-UUID
dmctl setup issuer device-status -device-id DEVICE-UUID
# Retry ordinary device identity renewal without a rollover revision:
dmctl setup issuer retry -device-id DEVICE-UUID
# After the cohort is complete and no enabled device depends on revision 1:
dmctl setup issuer retire -revision 1
```

Retirement closes the old issuing routes and removes its device trust, while
keeping material needed for certificate status publication until issuer expiry.
An offline enabled device blocks retirement. Resolve or explicitly disable its
enrollment through the existing administrative controls; elapsed time is not
migration confirmation. Complete and retire one issuer transition before starting
another. APNs renewal proceeds independently of this cohort.

The lab HTTPS trust CA is a separate identity. Its worker prepares a successor
and tracks acknowledgement of an overlapping HTTPS trust profile on enabled
devices. It keeps the old HTTPS certificate until that cohort confirms trust,
then activates the new CA and HTTPS leaf. New enrollment profiles include pending
HTTPS trust. An exported profile alone is not confirmation. Inspect or control
this workflow with `issuer status`, `issuer rollover`, `issuer retry`, and `issuer
retire` using `-id https-ca`. Public HTTPS with a system-trusted CA avoids this lab
trust distribution step.

## Adopt an existing lab

Back up the existing database and its secret files first. For a live SQLite bench:

```sh
dmctl setup adopt -from-bench test-lab/local/certs/bench \
  -dir test-lab/local/certs/managed -role customer
dmctl setup check -setup-file test-lab/local/certs/managed/setup.json
```

This reuses the existing database, preserves both bench encryption key IDs,
retains the ACME identifier HMAC derivation, admission policy, admin credential,
push topic, and all existing certificate keys. It adopts the current lab CA under
both existing uses (HTTPS trust and enrollment); fresh deployments generate
separate CAs. It does not stop the running server. Switch the listener to
`dmserver --setup-file ...` after checking the adopted state. No profile reinstall
is required for adoption of the same certificates.

For other deployments, initialize with the existing `-storage-key-file` and
`-storage-key-name`, preserve existing `-admin-token-file`, `-issuance-key-file`,
and `-acme-key-file` as applicable, retain the original database and security
settings, and adopt each identity with `setup adopt -kind KIND -id ID -cert FILE
-key FILE`. Importing a new certificate is a separate pending revision, rather than
an adoption over an existing identity.

## Remote API and library integration

Clear `DM_SETUP_FILE` and omit `-setup-file` to use the configured `dmctl` remote
context. Remote setup uses the existing client certificate verification and
credential-file references. Read operations require `readCertificateSetup`;
mutations use `manageVendorSigning`, `managePushCertificates`,
`manageHTTPSCertificates`, or `manageEnrollmentIssuers` as listed by the server's
action catalogue. Deployment-role checks apply in addition to authorization.

The API exposes `GET /setup`, workflow metadata/history/public exports, and
`POST /setup/{vendor|push|https|issuer}/{operation}` beneath `/admin/v1`. Request
fields include `id`, `revision`, `subject`, `dnsNames`, `certificate`, `csr`,
`signedRequest`, `device`, and `publicAcme`. Binary JSON fields are base64-encoded.
Responses contain metadata and explicitly requested public artifacts, never
stored private keys.

Library callers construct `lifecycle.Manager{Store: repository}` and invoke
`Begin`, `Export`, `Sign`/`AttachSignature`, `Import`, and `Activate`. Use encrypted
persistent adapters for repository values. `Publish` projects an activated
identity into runtime storage inside the same transaction. `LoadMaterial` is a
privileged in-process runtime interface; keep it behind application authorization.
`WithAudit` supplies authenticated actor attribution. Scheduling is application
owned; constructing a manager starts no goroutines.

## Validation

The suites cover same-topic annual renewal through the original certificate's
expiry, concurrent/retried requests, encrypted SQL activation and rollback,
bootstrap recovery, authorization, offline rollover blockers, issuer retirement,
and delayed device confirmation. SQL coverage uses SQLite, PostgreSQL, and MySQL.
A [Pebble](https://github.com/letsencrypt/pebble) integration test performs actual
HTTP-01 validation and recovers after a deliberately lost account response:

```sh
PEBBLE_BINARY=/path/to/pebble PEBBLE_SOURCE=/path/to/pebble-checkout \
  go test -tags=integration -run TestPebbleHTTP01Recovery ./devicemanagement/pki/lifecycle
```

Use Pebble v2.8.0. Its generated test certificates do not validate production CA
issuance or the Apple portal. Annual Apple renewal needs a real returned Apple
certificate; automated tests use a synthetic signing authority and clock.
