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

## The four managed identities

Each identity is named for what it is. The name is the command group, the identity's default
ID and the admin route segment, so one word is enough to say which certificate is meant.

| Name | Issued by | Purpose |
|---|---|---|
| `vendor-signing` | Apple Developer | Signs a customer's push certificate request |
| `mdm-push` | Apple Push Certificates Portal | Carries the APNs topic that wakes devices |
| `server-https` | The operator, or a public ACME CA | The server's own TLS identity |
| `enrollment-ca` | The operator | Issues device identities at enrollment |

A deployment's `role` is a separate setting: `vendor` and `customer` say which half of the
signing exchange it performs. See
[decision 0059](../research/decisions/0059-managed-certificate-lifecycle.md).

## The server HTTPS identity and the enrollment CA

A local lab can create its HTTPS identity and a separate HTTPS trust CA:

```sh
dmctl setup server-https lab -cn 'Local MDM HTTPS' -hosts localhost,127.0.0.1,::1
dmctl setup server-https activate -revision 1
dmctl setup profile trust -out test-lab/local/certs/profiles/managed-https-trust.mobileconfig
dmctl setup enrollment-ca create -cn 'MDM enrollment CA'
dmctl setup enrollment-ca activate -revision 1
```

New lab CAs default to ten years. The HTTPS leaf uses a distinct private key and
is renewed automatically under the same lab CA. Install the HTTPS trust profile
on lab devices before connecting them. A public CA-issued HTTPS identity normally
needs no custom HTTPS trust profile.

For public HTTP-01 issuance, configure `setup.http01Listen` in `setup.json` (or
supply `-http01-listen :80` at initialization). Configure public DNS and route port
80 to the challenge listener. Then run:

```sh
dmctl setup server-https acme -cn mdm.example.com -hosts mdm.example.com \
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
dmctl setup server-https request -cn mdm.example.com -hosts mdm.example.com
dmctl setup workflow export -id server-https -artifact csr -out https.csr.pem
dmctl setup server-https import -revision 1 -cert https-chain.pem
dmctl setup server-https activate -revision 1
```

HTTPS imports must match the pending key, requested hostnames, validity period,
and trusted chain. An existing HTTPS identity can instead be adopted with
`setup adopt -kind server-https -id server-https -cert chain.pem -key key.pem`.

## The vendor-signing identity

Run these commands on the vendor deployment, or in the combined lab:

```sh
dmctl setup vendor-signing request -cn 'Example MDM Vendor' -organization 'Example Ltd'
dmctl setup workflow export -id vendor-signing -artifact csr -out vendor.certSigningRequest
```

**Operator step:** In the [Apple Developer certificate portal](https://developer.apple.com/account/resources/certificates/list),
create an **MDM Vendor CSR Signing Certificate**, upload this CSR, and download
the certificate. This requires the applicable Apple developer account access.
Follow Apple's [vendor certificate instructions](https://developer.apple.com/help/account/certificates/mdm-vendor-csr-signing-certificate/).

```sh
dmctl setup vendor-signing import -revision 1 -cert vendor.cer
dmctl setup vendor-signing activate -revision 1
```

The importer accepts DER or PEM and completes Apple's issuer chain from the
bundled public authorities. It verifies the key, validity, signing purpose,
ordered chain, and trust anchor. The legacy Apple root's SHA-1 self-signature is
not revalidated as an issued certificate; signatures of issued chain members
still undergo normal verification.

## The MDM push identity

Run on the customer deployment or combined lab:

```sh
dmctl setup mdm-push request -cn 'Example MDM Customer' -organization 'Example Ltd' \
  -account mdm-owner@example.com
dmctl setup mdm-push sign -revision 1
```

In a combined deployment, signing uses the active local vendor identity. A
customer can configure `setup.vendorUrl` and `setup.vendorTokenFile` to call a
vendor's HTTPS setup API. Give that credential permission for `signCustomerPushRequests` (signing only).
Only the public CSR crosses that boundary; the customer private key remains in
customer storage. Redirects cannot forward the signing credential.

For an offline exchange between separate deployments:

```sh
# Customer deployment
dmctl setup workflow export -id mdm-push -artifact csr -out customer.csr.pem

# Vendor deployment, using its own setup file
dmctl setup vendor-signing sign -csr customer.csr.pem -out customer.signed.csr

# Customer deployment
dmctl setup mdm-push sign -revision 1 -signed-request customer.signed.csr
```

The customer verifies the returned signature, vendor chain, and exact CSR before
saving the portal artifact.

```sh
dmctl setup workflow export -id mdm-push -artifact signed-request -out customer.signed.csr
```

**Operator step:** Upload the signed request to the [Apple Push Certificates Portal](https://identity.apple.com/pushcert/)
and download the MDM push certificate. The portal returns the certificate only;
the matching private key remains in the customer deployment that created the
CSR. Record which Apple account owns it. This follows Apple's
[customer push setup workflow](https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers).
The vendor signature is produced by
[`pushcert.SignCSR`](../../devicemanagement/pki/pushcert/csr.go), using the vendor's
signing key, never the customer's private key.

```sh
dmctl setup mdm-push import -revision 1 -cert downloaded-mdm-push.pem
dmctl setup mdm-push activate -revision 1
dmctl setup check
dmserver --setup-file "$DM_SETUP_FILE"
```

`push import` validates the certificate against the retained pending key and
stores the revision as `ready`. It leaves the active runtime credential in place.
The separate `push activate` operation revalidates that ready revision and
publishes it. In the reference server, workflow activation and the runtime push
certificate/version commit in one SQL transaction; a publication error rolls
both back. See [`Manager.Import` and `Manager.Activate`](../../devicemanagement/pki/lifecycle/certificates.go)
and [runtime certificate publication](../../server/statestore/certificates.go).

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
dmctl setup workflow show -id mdm-push
dmctl setup workflow history -id mdm-push
dmctl setup mdm-push renew
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

When certificate lifecycle and enrollment are configured, the reference worker
scans enabled device enrollments every minute. It starts ordinary identity renewal
within sixty days of the recorded certificate's expiry, using retained issuance
evidence and the original enrollment profile. Active issuer rollover takes priority;
blocked renewal attempts require inspection and explicit retry. This worker already
exists independently of any proposed fleet reconciler. See
[`renewidentities.go`](../../server/internal/app/renewidentities.go).

Device identities nearing expiry use the authenticated enrollment replacement
handshake. The server retains the original management endpoint, topic, profile
identity and access rights. It tracks issuance, candidate authentication,
TokenUpdate, and command acknowledgement before committing the new pin. Expiry of
the short-lived issuance grant does not discard confirmation for an already
issued candidate; no new key or CSR can claim the expired grant.

Locally managed enrollment roots prepare a successor before expiry. External
issuers require a returned certificate from their CA. To prepare manually:

```sh
dmctl setup enrollment-ca renew -force
# For a locally managed root; for an external issuer, export its CSR and import the chain.
dmctl setup enrollment-ca create -revision 2
dmctl setup enrollment-ca rollover -revision 2
dmctl setup enrollment-ca status -revision 2
```

Rollover installs issuer-specific SCEP and device ACME routes, trusts both CAs,
switches the default for new profiles, and tracks existing devices separately.
Each device first acknowledges an overlapping trust profile, then receives its
replacement identity. Offline devices remain in persistent migration state.
Unsupported profile installation, missing original profile metadata, failed
commands, or unknown issuer evidence produce an explicit blocker.

```sh
dmctl setup enrollment-ca retry -revision 2 -device-id DEVICE-UUID
dmctl setup enrollment-ca device-status -device-id DEVICE-UUID
# Retry ordinary device identity renewal without a rollover revision:
dmctl setup enrollment-ca retry -device-id DEVICE-UUID
# After the cohort is complete and no enabled device depends on revision 1:
dmctl setup enrollment-ca retire -revision 1
```

Retirement closes the old issuing routes and removes the issuer from the server's
accepted device CAs, while keeping material needed for certificate status
publication until issuer expiry. It does not remove root payloads already
installed in device profiles.
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
retire` using `-id server-https-ca`. Public HTTPS with a system-trusted CA avoids this lab
trust distribution step.

Administrative clients also need the pending lab HTTPS CA before leaf activation.
Export its public certificate over the currently verified connection, check its
fingerprint against workflow metadata, and add it to the client's existing trust
bundle. Keep the old CA in that bundle during overlap. Verify the new HTTPS leaf
and fresh device/user command acknowledgements before retiring the old CA.

For a recovery drill, preserve the latest confirmed device identity together with
the database, external encryption keys and their IDs, setup file, secret references,
and admission policy. Stop all writers before taking the recovery checkpoint.
Verify the authenticated backup and server configuration in isolation, then restore
into an empty location with the same public URL and identities. Confirm fresh
commands on both channels and DDM status after restart. A checkpoint predating a
completed device identity replacement cannot establish continuity for that new
identity. Use the implemented `dmctl recovery` pause, backup, verify, restore and
resume commands in the [recovery guide](recovery.md). Archive verification and
device continuity are separate checks; off-machine recovery requires copying the
archive and separately held recovery identity to the intended recovery host.

## FileVault encryption identities

The reference server generates and retains a distinct RSA-2048 encryption identity
before queueing a `RotateFileVaultKey` command or an escrow profile through
`POST /admin/v1/enrollments/device/DEVICE-ID/filevault/escrow`. The key and self-signed
certificate are generated entirely in Go and committed to encrypted SQL state.
No manual certificate preparation or OS certificate tool is required.

The escrow profile supports Apple's macOS 26 bootstrap-token rotation flow. The
explicit rotation command still requires Apple's unlock credentials. See the
[workflow and prerequisites](protocol-helpers.md#automatic-filevault-encryption-certificates)
and [identity-retention decision](../research/decisions/0054-filevault-encryption-identities.md).

These identities are separate from the enrollment issuer and HTTPS/APNs identities.
Retain the exact private key after certificate expiry for delayed encrypted replies.
An escrow profile can keep producing material for its certificate, so completing
`InstallProfile` does not make its key disposable. Keep the database, storage-key
names and storage-key material together in recoverable backups. A backup predating
a FileVault rotation does not contain the newly escrowed recovery key.

## Adopt an existing lab

Back up the existing database and its secret files first. For a live SQLite lab workspace:

```sh
dmctl setup adopt -from-lab test-lab/local/certs/lab \
  -dir test-lab/local/certs/managed -role customer
dmctl setup check -setup-file test-lab/local/certs/managed/setup.json
```

This reuses the existing database, preserves both lab encryption key IDs,
retains the ACME identifier HMAC derivation, admission policy, admin credential,
push topic, and all existing certificate keys. It adopts the current lab CA under
both existing uses (HTTPS trust and enrollment); fresh deployments generate
separate CAs. It does not stop the running server. Switch the listener to
`dmserver --setup-file ...` after checking the adopted state. No profile reinstall
is required for adoption of the same certificates.

For other deployments, initialize with the existing `-storage-key-file` and
`-storage-key-name`, preserve existing `-bootstrap-token-file`, `-issuance-key-file`,
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
