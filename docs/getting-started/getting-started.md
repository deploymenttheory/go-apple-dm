# Getting started with go-apple-dm

This guide takes you from a source checkout to a running reference server, a
simulated enrollment, and a first manually enrolled Apple device. It then shows
how to use the library in your own Go application and which responsibilities
move to you when you replace the reference composition.

The project is a pre-1.0 library, reference server, and administrative CLI. It
provides protocol handling and integration components; you supply deployment
infrastructure, Apple credentials, enrollment policy, and operational ownership.
There is no fleet inventory UI or administrative username/password sign-in.

The instructions describe the source in this checkout. Earlier releases may
have different APIs and defaults. Use the documentation shipped with the
revision you deploy. Apple-specific requirements were checked on 2026-09-11;
linked Apple pages remain the authority for platform availability and account
requirements.

## Contents

1. [Choose a starting point](#1-choose-a-starting-point)
2. [Prepare the tools and checkout](#2-prepare-the-tools-and-checkout)
3. [Start the server locally](#3-start-the-server-locally)
4. [Exercise simulated enrollment](#4-exercise-simulated-enrollment)
5. [Keep state across restarts](#5-keep-state-across-restarts)
6. [Bootstrap stored administrators](#6-bootstrap-stored-administrators)
7. [Prepare real-device enrollment](#7-prepare-real-device-enrollment)
8. [Enroll a device and retrieve inventory](#8-enroll-a-device-and-retrieve-inventory)
9. [Add enrollment modes and DDM](#9-add-enrollment-modes-and-ddm)
10. [Deploy and operate the server](#10-deploy-and-operate-the-server)
11. [Use the library in your application](#11-use-the-library-in-your-application)
12. [Troubleshoot by milestone](#12-troubleshoot-by-milestone)

## 1. Choose a starting point

| Your first goal | What you need | Completion evidence |
|---|---|---|
| Build commands or inspect schema support | Go and the library, or the built `dmctl` | A generated plist or offline `explain` output |
| Start the reference server | Go, a source checkout, and a free loopback port | Healthy/ready responses and authenticated `dmctl status` |
| Exercise enrollment and command delivery locally | The reference-server bench and its generated fixtures | A passing simulated scenario with retained results |
| Enroll an actual device | Persistent storage and keys, trusted HTTPS, an MDM APNs identity, an issuer, admission policy, and an authorized test device | Issued identity, Authenticate, TokenUpdate, and an acknowledged inventory command |
| Use Automated Device Enrollment (ADE) | The real-device prerequisites plus organizational device assignment and a device enrollment service token | A device completes the assigned Setup Assistant enrollment flow |
| Use account-driven enrollment | The real-device prerequisites plus Managed Apple Accounts, service discovery, and an identity provider | Account authentication, certificate association, and ongoing authenticated management |

No Apple account, APNs certificate, public DNS, or physical device is needed for
the first three rows. A healthy server alone does not establish device
interoperability. Simulation verifies the modeled exchanges; physical devices
must validate your chosen OS, hardware, enrollment mode, and trust configuration.

Start with the `all` role. It combines MDM, enrollment, the DDM engine, and the
administrative services in one process. Split roles add infrastructure work and
are covered after the first-device walkthrough.

## 2. Prepare the tools and checkout

The commands below use a POSIX-style shell on macOS or Linux and run from the
repository root unless stated otherwise. CI exercises Go tests on both platforms.
You do not need to run the server on macOS to manage Apple devices.

| Tool or resource | Requirement |
|---|---|
| Go | **1.27 or newer compatible toolchain**, as declared in both `go.mod` files and `go.work`; install the matching toolchain before building |
| Git | Clone the repository and initialize its pinned Apple schema submodule |
| Network access during build | Reach the configured Go module proxy/checksum service and GitHub, or provide your organization's approved caches |
| `make` | Used by the maintained build, bench, and validation commands |
| `curl` | Used for health and public endpoint checks |
| OpenSSL 3 | Used below to generate private random material and an optional lab issuer |
| C compiler | Needed for Go's race detector when running the race-enabled test targets; ordinary server builds use a pure-Go SQLite driver |
| Docker | Optional for the image and PostgreSQL/MySQL integration fixtures; unnecessary for the default SQLite bench |
| Xcode and Apple app-signing material | Only for the optional native app-push fixture, not for the server or ordinary MDM enrollment |

```sh
git clone --recurse-submodules https://github.com/deploymenttheory/go-apple-dm.git
cd go-apple-dm
go version
git submodule status

mkdir -p bin
go build -o bin/dmserver ./server/cmd/dmserver
go build -o bin/dmctl ./server/cmd/dmctl
./bin/dmctl -h
```

For an existing checkout, run `git submodule update --init --recursive`. Do not
advance the Apple submodule to its latest branch: generation is tied to the
recorded commit. Generated schema code is already committed, so an adopter does
not need to regenerate it to build the binaries.

`go.work` joins the root library and `server` module. Keep workspace mode enabled
when building this checkout so the server uses the accompanying library code.
Installing the modules into another application is covered in
[section 11](#11-use-the-library-in-your-application).

## 3. Start the server locally

In a fresh terminal without earlier deployment-specific `DM_*` settings, run:

```sh
DM_ROLE=all DM_STORAGE=inmem DM_LISTEN=127.0.0.1:8080 \
  DM_ADMIN_TOKEN=dev-token ./bin/dmserver
```

The process stays in the foreground. In a second terminal at the repository root:

```sh
curl --fail --silent --show-error http://127.0.0.1:8080/healthz
curl --fail --silent --show-error http://127.0.0.1:8080/readyz
./bin/dmctl -server http://127.0.0.1:8080 -token dev-token status
./bin/dmctl -server http://127.0.0.1:8080 -token dev-token routes
./bin/dmctl -server http://127.0.0.1:8080 -token dev-token enrollments list
```

Both probes should return HTTP 200. `/healthz` checks storage; `/readyz` also
requires the configured workers to be running. `status` should report role
`all`, the available API families, and accepted authorization modes. An empty
enrollment list is expected.

The CLI's `api` command addresses paths relative to `/admin/v1`; for example,
`dmctl api GET /enrollments` calls `/admin/v1/enrollments`. Public routes such as
`/healthz` and `/MDMServiceConfig` are queried directly with `curl`.

This explicit `dev-token` is only for the disposable loopback example. It grants
unrestricted access. `inmem` loses every enrollment, queue, credential, and policy
on restart. Enrollment routes require additional configuration, and pushes are
off by default. Stop this process with **Ctrl-C** before continuing to another
server on the same port.

Some useful commands require no server or credential:

```sh
./bin/dmctl explain DeviceInformation
./bin/dmctl explain DeviceInformation -target macos:15.0,supervised
./bin/dmctl explain com.apple.configuration.softwareupdate.enforcement.specific
```

The target above is an example to inspect, not a declaration that the connected
device has those capabilities.

## 4. Exercise simulated enrollment

The bench supplies local APNs, device enrollment service, Apple Business Manager,
OIDC, and attestation fixtures around the ordinary `dmserver` binary. It prepares
TLS and persistent state for you. Use a separate workspace from any existing live
lab; the path below is ignored by Git.

In terminal one:

```sh
make bench-init BENCH_WORKSPACE=test-lab/local/getting-started-sim \
  BENCH_MODE=simulated BENCH_STORAGE=sqlite BENCH_TOPOLOGY=all \
  BENCH_LISTEN=127.0.0.1:8443
make bench-up BENCH_WORKSPACE=test-lab/local/getting-started-sim
```

In terminal two:

```sh
make bench-status BENCH_WORKSPACE=test-lab/local/getting-started-sim
make bench-list
make bench-run BENCH_WORKSPACE=test-lab/local/getting-started-sim BENCH_SCENARIO=E2E-006
make bench-run BENCH_WORKSPACE=test-lab/local/getting-started-sim BENCH_SCENARIO=E2E-014
make bench-run BENCH_WORKSPACE=test-lab/local/getting-started-sim BENCH_SCENARIO=E2E-008
make bench-down BENCH_WORKSPACE=test-lab/local/getting-started-sim
```

These scenarios exercise SCEP enrollment and command delivery, ACME attestation,
and DDM synchronization respectively. Configuration-specific scenarios may start
an isolated temporary instance of the same executable. They do not change your
live configuration or enroll the host computer.

Look for passing results and the JSON/JUnit evidence under
`test-lab/local/getting-started-sim/evidence/`. `BENCH_SCENARIO=all` runs all
scenarios supported by the workspace mode. Use the
[catalogue](../testing/bench-catalogue.md) to choose smaller runs.

Initialize a workspace only once. Later sessions use `bench-up` with the same
path. Initialization refuses to overwrite existing configuration or incomplete
identity material. Keep its keys with its database; changing `BENCH_MODE` on a
later Make invocation does not rewrite an existing `bench.json`.

## 5. Keep state across restarts

Use SQLite for a first persistent, single-host installation. The reference
server defaults to SQLite, but deliberately refuses persistent storage without
encryption key configuration. PostgreSQL and MySQL are alternatives described
in [section 10](#10-deploy-and-operate-the-server).

Create a private directory for this walkthrough. In each terminal that uses it,
set `GS_DIR` from the repository root:

```sh
export GS_DIR="$PWD/test-lab/local/getting-started"
umask 077
mkdir -p "$GS_DIR/secrets"

if [ ! -e "$GS_DIR/secrets/main" ]; then
  openssl rand -hex 32 > "$GS_DIR/secrets/main"
fi
if [ ! -e "$GS_DIR/bootstrap-token" ]; then
  openssl rand -hex 32 > "$GS_DIR/bootstrap-token"
fi
```

Generate these values once and preserve them. `main` is the storage key's name;
the file contains random key material. `DM_SECRETS_DIR` resolves a file whose
name exactly matches each entry in `DM_STORAGE_KEYS`. Without a secrets
directory, the alternative here is `DM_STORAGE_KEY_MAIN`. Storage key material
is consumed as bytes and derived with HKDF; hexadecimal text is not hex-decoded.

Create `$GS_DIR/server.env` with this content:

```sh
export DM_ROLE=all
export DM_LISTEN=127.0.0.1:8080
export DM_STORAGE=sqlite
export DM_DSN="$GS_DIR/dm.sqlite"
export DM_STORAGE_KEYS=main
export DM_SECRETS_DIR="$GS_DIR/secrets"
export DM_ADMIN_STORE=true
export DM_ADMIN_TOKEN="$(cat "$GS_DIR/bootstrap-token")"
export DM_AUDIT_STORE=true
export DM_AUDIT_RETENTION=720h
```

This is a shell environment file that you source yourself. `dmserver` does not
automatically read `.env` files. Its paths use the `GS_DIR` you exported. The
30-day audit retention is a walkthrough choice; configure retention for your
own requirements. `DM_SECRETS_DIR` supplies storage keys only: it does not
automatically populate other credential variables, and `DM_ADMIN_TOKEN` takes
the token itself, not `@file`.

```sh
chmod 600 "$GS_DIR/server.env"
. "$GS_DIR/server.env"
./bin/dmserver
```

The SQL stores apply their embedded migrations on open. The directory must be
writable by the server account. In another terminal:

```sh
export GS_DIR="$PWD/test-lab/local/getting-started"
export DMCTL_SERVER=http://127.0.0.1:8080
export DMCTL_TOKEN="@$GS_DIR/bootstrap-token"
./bin/dmctl status
./bin/dmctl principals list
./bin/dmctl audit list --since 1h
```

`DMCTL_TOKEN` supports a literal, `@path`, or `env:VARIABLE_NAME`. Prefer a file or
environment reference over putting a real token in command arguments. Keep files
private and avoid shell tracing while handling credentials.

Selected secret columns, raw protocol messages, commands/results, protocol state,
and credential-bearing declaration data are encrypted in the reference SQL
composition. Metadata and status/audit records are not a fully encrypted database.
Protect the whole database and backups. A database backup without its original
key names and material cannot restore encrypted values. Do not generate new
material under an existing key name to repair a startup error.

## 6. Bootstrap stored administrators

The principal store starts empty and its Cedar policies deny by default.
`DM_ADMIN_TOKEN` exists to bootstrap it: this static token acts as root and
bypasses policy. Stored principals, including roots, require Cedar permissions
for policy-controlled operations.

With the persistent server running and the CLI using the bootstrap token,
create a root principal. The explicit human output mode writes only the new
token to stdout; the explanatory message goes to stderr.

```sh
umask 077
./bin/dmctl -output human principals create operator-root --root > "$GS_DIR/operator-root-token"
```

Run token-creation commands once using a new output path. Later reads cannot
recover the plaintext token. A failed command with shell redirection can still
truncate an existing output file.

Create `$GS_DIR/operator-root.cedar`:

```cedar
permit (
    principal == MDM::Principal::"operator-root",
    action,
    resource
);
```

Install the policy, switch credentials, and test an operation that requires
policy authorization:

```sh
./bin/dmctl policies put operator-root --file "$GS_DIR/operator-root.cedar"
export DMCTL_TOKEN="@$GS_DIR/operator-root-token"
./bin/dmctl principals list
./bin/dmctl policies list
```

`status` and `routes` are authenticated introspection operations; successful
introspection alone does not prove the principal can administer the server.
Keep access to the bootstrap configuration until the new credential and policy
have been tested.

For a scoped reader, create `$GS_DIR/inventory-reader.cedar`:

```cedar
permit (
    principal in MDM::Role::"inventory-reader",
    action == MDM::Action::"readEnrollment",
    resource
);
```

```sh
./bin/dmctl policies put inventory-reader --file "$GS_DIR/inventory-reader.cedar"
./bin/dmctl -output human principals create inventory-reader \
  --roles inventory-reader > "$GS_DIR/inventory-reader-token"
./bin/dmctl -token "@$GS_DIR/inventory-reader-token" enrollments list
```

Role names have no built-in permissions; the policy above supplies them. Review
available actions with `dmctl actions`. Principal creation/update, token
rotation, revocation, deletion, and policy administration require root. A scoped
principal cannot rotate its own token, even if a Cedar policy permits that action.

After verifying stored administration:

1. Stop the server with Ctrl-C.
2. Remove the `export DM_ADMIN_TOKEN=...` line from `server.env`.
3. In the server terminal, run `unset DM_ADMIN_TOKEN`, source the edited file,
   and restart `./bin/dmserver`. Removing a line does not unset an already
   exported shell variable.
4. With `DMCTL_TOKEN` pointing to the root token, check `dmctl status` and
   `dmctl principals list`. Status should report policy enabled and break-glass
   access disabled. These checks also establish that credentials and policies
   survived the restart.

The simple `principals create` and `rotate` commands do not set expiry. Use the
JSON administration API's `ExpiresAt` field when you need expiring credentials.
Rotation invalidates the previous token. Maintain a tested recovery procedure
and rotate before expiry: the last-active-root guard prevents immediate removal
of the final active root but cannot prevent future natural expiry or a policy
that denies every root. See the [administration decision](../research/decisions/0034-admin-api-and-authorization.md).

## 7. Prepare real-device enrollment

### 7.1 Choose the first device and enrollment method

Use a device whose enrollment you are authorized to change and record its OS
version, hardware, serial number, and enrollment identifier. The first example
below uses manually installed, profile-based Device Enrollment with SCEP. Apple
supports SCEP and recommends ACME for device identities; SCEP is the reference
server's default and avoids assuming the first device can attest. ACME setup
follows in [section 9](#9-add-enrollment-modes-and-ddm).
[Apple's certificate guidance](https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices)
requires distinct device identities and a certificate-to-device association.

For a Mac, obtain the hardware UUID and serial from System Information. The
UUID is also reported as `IOPlatformUUID` by:

```sh
ioreg -rd1 -c IOPlatformExpertDevice
```

For other devices, obtain the actual UDID through an appropriate trusted device
inventory or Apple tool. Do not substitute a serial number for `DeviceID`. An
account-driven enrollment has different identifier and account semantics.

Manual enrollment does not establish ADE ownership or exercise Setup Assistant.
Review the existing management profile and the effect of removing it before
using an already managed device. Apple's
[enrollment-method comparison](https://support.apple.com/guide/deployment/dep08f54fcf6/web)
describes supervision, privacy, and command availability by method.

### 7.2 Prepare DNS, HTTPS, and network paths

Choose a stable device-facing base URL such as `https://mdm.example.com`. Use a
certificate whose DNS subject alternative name matches that hostname and whose
chain the devices trust. The server does not obtain or renew its public TLS
certificate automatically.

| Connection | Required preparation |
|---|---|
| Device to reference server | DNS and verified HTTPS to the public endpoint, conventionally TCP 443; forward the enrollment, MDM, ACME/SCEP, and certificate-status routes needed by your deployment |
| Administrator to server | Verified HTTPS remotely; use `DMCTL_CA_FILE` for private trust |
| Server to APNs | Outbound TLS to Apple's production APNs service; this client defaults to `https://api.push.apple.com` on 443 |
| Device to APNs | Persistent outbound connectivity, normally TCP 5223 with Apple's documented 443 fallback |
| Device/server to other services | Enrollment activation, attestation, DEP, OIDC, or Apple Business/School Manager endpoints as required by the selected flow |

Use Apple's [APNs connectivity guidance](https://support.apple.com/en-us/102266)
and [enterprise network requirements](https://support.apple.com/en-us/101555)
for the current host/port lists. Do not apply HTTPS interception to the Apple
services those documents exclude. APNs tells the device to contact the server;
it does not deliver the MDM command itself. Apple does not initiate an inbound
connection to your server to carry the command.

There are two supported public HTTPS arrangements:

- **Native TLS:** configure `DM_TLS_CERT_FILE` and `DM_TLS_KEY_FILE`, and use a
  non-loopback `DM_LISTEN` only with both configured. The example below uses
  `0.0.0.0:8443`, with infrastructure forwarding public port 443 to it while
  preserving TLS. If devices connect directly on 8443, include that port in
  `DM_PUBLIC_URL` and the admin URL.
- **TLS proxy:** terminate public TLS at a controlled proxy and forward to a
  literal-loopback HTTP listener on the same host, or to a backend using
  verified TLS. Preserve the request body and `Mdm-Signature`. Configure
  certificate-header forwarding only when the proxy validates client
  certificates, strips incoming spoofed headers, and is restricted by
  `DM_TRUSTED_PROXIES`. See [identity and transport](../operations/enrollment-security.md#identity-transport-and-storage).

The shared native listener verifies client certificates when supplied; it allows
preidentity enrollment and administration to reach their respective handlers.
Do not configure a blanket mandatory client certificate at your public proxy
that prevents a new device obtaining its first identity. CMS identity through a
proxy is an Apple-documented path.

For privately trusted HTTPS, set `DM_ENROLL_TLS_ANCHOR_FILE` to the public trust
bundle included in enrollment profiles. The device must already trust HTTPS
before downloading a profile from that endpoint; use a verified, separately
delivered trust profile or your organization's existing trust deployment.
`DMCTL_CA_FILE` affects only the CLI, not the device. A root certificate is trust
material; its private key is never distributed to devices.

### 7.3 Obtain the MDM APNs identity

An MDM push identity is separate from your HTTPS certificate, enrollment CA,
ordinary app APNs certificate, and Apple API keys. Apple MDM requires
certificate-based APNs authentication; a token-based APNs `.p8` key cannot
replace the MDM certificate. Apple's
[MDM push setup procedure](https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers)
describes the customer CSR, vendor signature, and portal exchange.

If you already have a valid MDM certificate and its matching private key, skip
CSR generation and validate the pair. Otherwise:

1. Generate a customer key and CSR on the customer/server side, using new paths:

   ```sh
   ./bin/dmctl pushcerts csr --cn 'Example organization MDM' \
     --key-out "$GS_DIR/push.key" --csr-out "$GS_DIR/push.csr"
   ```

2. Arrange signing by an authorized MDM vendor. If operating your own vendor
   infrastructure, obtain Apple's approval and MDM Vendor CSR Signing
   Certificate through the
   [Account Holder process](https://developer.apple.com/help/account/certificates/mdm-vendor-csr-signing-certificate).
   `dmctl pushcerts sign -h` describes the signing command. It requires the
   customer CSR, vendor signing key, complete vendor certificate chain, and
   trusted Apple roots. Run that operation within the vendor's infrastructure.

3. Upload the signed request to the
   [Apple Push Certificates Portal](https://identity.apple.com/pushcert/), then
   save the issued MDM certificate with the original customer key. Apple
   recommends a Managed Apple Account for the portal. Record the account and
   certificate renewal ownership.

Keep the customer private key on the customer instance and the vendor signing
key within vendor infrastructure. The repository cannot grant the Apple
capability, issue the Apple-signed push certificate, or reconstruct a lost
private key. Use a separate customer push certificate per customer.

Validate and, if necessary, normalize a downloaded PEM or DER certificate into
a **new** file at `$GS_DIR/push.pem`:

```sh
./bin/dmctl apns check --kind mdm --cert /path/to/downloaded-mdm-certificate \
  --key "$GS_DIR/push.key" --cert-out "$GS_DIR/push.pem"
./bin/dmctl apns inspect --kind mdm --cert "$GS_DIR/push.pem"
```

If `push.pem` already exists, omit `--cert-out` and check that file directly.
Record the reported topic, normally `com.apple.mgmt.External.…`, and expiration.
Set `DM_PUSH_TOPIC` to that exact topic. The topic placed in the enrollment
profile and the APNs identity must agree. These offline checks verify certificate
properties and key matching; successful delivery still requires the live checks
in section 8.

### 7.4 Prepare an enrollment issuer and admission policy

Persistent enrollment requires a valid CA certificate and its matching,
unencrypted PEM private key. Follow your PKI policy for a deployed issuer. The
reference composition loads the issuer from files; an embedder can supply a
different signing implementation.

For a **new lab issuer only**, OpenSSL can generate an RSA CA suitable for this
SCEP walkthrough. Preserve existing issuer material instead of running this
again over it:

```sh
openssl req -x509 -newkey rsa:3072 -sha256 -noenc -days 3650 \
  -subj '/CN=go-apple-dm Lab Enrollment CA' \
  -addext 'basicConstraints=critical,CA:TRUE,pathlen:0' \
  -addext 'keyUsage=critical,digitalSignature,keyEncipherment,keyCertSign,cRLSign' \
  -keyout "$GS_DIR/enrollment-ca.key" -out "$GS_DIR/enrollment-ca.pem"
```

The lifetime is a lab example. Issued certificates cannot outlive their issuer.
This CA is for device identities, not a substitute for the public HTTPS
certificate or the Apple-issued APNs certificate.

Create `$GS_DIR/admission.json` with the actual device identifiers:

```json
{
  "devices": [
    {"udid": "REPLACE-WITH-DEVICE-UUID", "serial": "REPLACE-WITH-SERIAL"}
  ]
}
```

Every configured identifier in a rule must match. Supply the same identifiers
when requesting the profile. For an initial UUID-only lab rule you can omit
`serial`; for ACME on a Mac include the serial as described in section 9.
An empty/missing policy denies issuance, including an administrator's request
for a profile. Policy files are loaded at startup; restart every replica after
changing one.

The service checks admission before profile delivery and again before issuance.
A reference SCEP profile contains an expiring, random credential bound to the
first accepted CSR. Setting a shared `DM_SCEP_CHALLENGE` does not authorize
reference-server enrollment. Treat downloaded profiles as credentials.

Create a stable ACME identifier key, even if initial profiles use SCEP: the
enrollment composition also serves ACME for secondary credentials.

```sh
if [ ! -e "$GS_DIR/acme-identifier-key" ]; then
  openssl rand -hex 32 > "$GS_DIR/acme-identifier-key"
fi
```

### 7.5 Configure and start the device-facing server

Stop the persistent server before changing its configuration. Add the following
settings to `server.env`, replacing all example hostnames, topic values, and TLS
paths. Keep the storage and principal-store settings from sections 5–6 and leave
the bootstrap token removed.

```sh
export DM_LISTEN=0.0.0.0:8443
export DM_TLS_CERT_FILE=/absolute/path/to/mdm-fullchain.pem
export DM_TLS_KEY_FILE=/absolute/path/to/mdm-tls.key
export DM_PUBLIC_URL=https://mdm.example.com
export DM_PUSH_TOPIC=com.apple.mgmt.External.REPLACE-WITH-CERTIFICATE-TOPIC
export DM_PUSH_SOURCE=file
export DM_PUSH_CERT_FILE="$GS_DIR/push.pem"
export DM_PUSH_KEY_FILE="$GS_DIR/push.key"

export DM_IDENTITY=scep
export DM_ENROLL_CA_CERT_FILE="$GS_DIR/enrollment-ca.pem"
export DM_ENROLL_CA_KEY_FILE="$GS_DIR/enrollment-ca.key"
export DM_CA_FILE="$GS_DIR/enrollment-ca.pem"
export DM_ENROLLMENT_POLICY_FILE="$GS_DIR/admission.json"
export DM_ACME_HMAC_KEY="@$GS_DIR/acme-identifier-key"
export DM_PROFILE_IDENTIFIER=com.example.mdm.enrollment
export DM_ORGANIZATION='Example organization'
```

For a private HTTPS CA, additionally set `DM_ENROLL_TLS_ANCHOR_FILE` to that
HTTPS CA bundle. Leave it unset for publicly trusted HTTPS. `DM_CA_FILE`, by
contrast, supplies trust for incoming **device identities**. The CA material in
those two settings need not be the same.

`DM_PUBLIC_URL` and `DM_PUSH_TOPIC` together enable enrollment. Use an absolute
HTTPS base URL with a host and no credentials, query, or fragment. For the first
setup, use a dedicated hostname without a path prefix. File-backed push
credentials load on startup. Leave Apple service endpoint and attestation-anchor
overrides unset for live use.

```sh
. "$GS_DIR/server.env"
./bin/dmserver
```

In the administration terminal, using the actual public URL:

```sh
export DMCTL_SERVER=https://mdm.example.com
export DMCTL_TOKEN="@$GS_DIR/operator-root-token"
# For private HTTPS only:
# export DMCTL_CA_FILE=/absolute/path/to/https-ca-bundle.pem

./bin/dmctl status
./bin/dmctl routes
curl --fail --silent --show-error https://mdm.example.com/healthz
curl --fail --silent --show-error https://mdm.example.com/readyz
curl --fail --silent --show-error https://mdm.example.com/MDMServiceConfig
```

For private HTTPS, give `curl` the corresponding `--cacert` argument as well.
Check that `routes` now includes the enrollment-profile API. Service discovery
should advertise your actual endpoint and configured trust information. Health
and readiness do not check Apple account approval or prove that APNs can reach a
device. `-insecure` is rejected by `dmctl`; configure the correct CA and hostname.

## 8. Enroll a device and retrieve inventory

Create `$GS_DIR/profile-request.json` using the device admitted in section 7:

```json
{
  "DeviceID": "REPLACE-WITH-DEVICE-UUID",
  "Serial": "REPLACE-WITH-SERIAL",
  "Identity": "scep",
  "AccessRights": 16
}
```

The inventory right is mask **16**. If you intend to test authorized profile
replacement, request **19** instead: inventory plus profile inspection and
installation/removal. Decide those rights before installing the initial profile;
replacement needs profile-installation rights already present.

With a new private output path, request the profile:

```sh
umask 077
./bin/dmctl -output human api POST /enrollment-profiles \
  --file "$GS_DIR/profile-request.json" > "$GS_DIR/enrollment.mobileconfig"
```

Only use the output if the command succeeds. Review its organization, device
binding, endpoint, topic, identity payload, and requested rights. The operator
endpoint returns an unsigned mobileconfig; a profile signing indicator is
separate from HTTPS trust and device-identity validation.

Transfer it through a protected channel and install it using the device's
supported manual profile workflow. On macOS, open the file and review the
downloaded profile in System Settings. Device UI labels vary by OS. This guide's
commands generate and download the profile; they do not install it on a device.

Expect this sequence:

1. The profile configures HTTPS trust and the identity request.
2. The device obtains its SCEP identity; the server records authorized issuance.
3. The device sends `Authenticate`; the service checks identity association and
   pins the certificate to the enrollment.
4. `TokenUpdate` supplies the push token and push magic. The device can now be
   woken for queued work.
5. An APNs wake causes the device to poll the MDM server, fetch a command, and
   return its response.

Check the server records, using the actual UUID in place of the placeholder:

```sh
./bin/dmctl enrollments list
./bin/dmctl enrollments get device REPLACE-WITH-DEVICE-UUID
./bin/dmctl api GET /enrollments/device/REPLACE-WITH-DEVICE-UUID/enrollment-evidence
```

Profile generation alone is not enrollment evidence. Look for the expected
issuer/identity method, certificate pin, and check-in timestamps.

Create `$GS_DIR/inventory.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CommandUUID</key><string>143441B0-345E-4E40-8C10-F68760FA2E21</string>
  <key>Command</key>
  <dict>
    <key>RequestType</key><string>DeviceInformation</string>
    <key>Queries</key>
    <array><string>OSVersion</string><string>BuildVersion</string></array>
  </dict>
</dict>
</plist>
```

Use a new UUID for each new command; the one above is for this first request.
Queue it, check the response's `Queued`/`Skipped` fields, and explicitly wake the
device. Queueing alone does not send a push from this administration endpoint.

```sh
./bin/dmctl commands send device REPLACE-WITH-DEVICE-UUID --file "$GS_DIR/inventory.plist"
./bin/dmctl push device REPLACE-WITH-DEVICE-UUID
./bin/dmctl commands list device REPLACE-WITH-DEVICE-UUID
./bin/dmctl api GET /enrollments/device/REPLACE-WITH-DEVICE-UUID/commands/143441B0-345E-4E40-8C10-F68760FA2E21/result
```

The result endpoint returns 204 while no result exists, 404 for an absent
command, or JSON with `Status`, base64-encoded plist `Response`, and any
`ErrorChain`. A first-device pass requires an `Acknowledged` result containing
`OSVersion` and `BuildVersion`. APNs acceptance is an intermediate result, not
proof of command delivery. Inspect `NotNow`, errors, and per-target skips rather
than treating every HTTP 200 as a successful command.

User-channel commands use their own enrollment ID and `--parent` device ID.
Do not assume every local Mac user is managed. Before commands requiring
supervision, ADE, or user-approved management, collect the relevant
`DeviceInformation`/`SecurityInfo` observations; unknown capabilities do not
satisfy those requirements.

The maintained [Mac enrollment runbook](../operations/mac-enrollment-testing.md)
adds separate ACME/SCEP and replacement exercises. Its live bench is designed for
a Mac reaching the bench's loopback endpoint. Use the device-facing deployment
above for other hosts/devices. Remove a test enrollment manually when finished
and confirm checkout/state cleanup; stopping the server does not unenroll a
device.

## 9. Add enrollment modes and DDM

### ACME and Managed Device Attestation

Retain the same storage, issuer, HTTPS, and admission preparation. Set:

```sh
export DM_IDENTITY=acme
export DM_ACME_KEY=ec384
export DM_ACME_POLICY=any
export DM_ACME_HMAC_KEY="@$GS_DIR/acme-identifier-key"
```

Persist these in the server configuration and restart. `any` still requires a
recognized issuance identifier, admission, and the configured attestation
checks; it does not admit arbitrary devices. `dep` additionally checks the
organizational device inventory, and `sip` adds a System Integrity Protection
requirement. Those two policies require the device enrollment service available
on the `all` role and appropriate synchronized inventory.

For an Apple silicon Mac profile request, set `Identity` to `acme`, include its
actual `Serial`, `Product`, and `OSVersion`, and set `MacHardware` to
`apple-silicon`. Other recognized hardware values are `t2` and `intel`. The
Mac's MDM UDID is distinct from the attested ProvisioningUDID; serial provides
the initial hardware binding. Supply actual hardware information rather than
guessing from a model string.

Apple documents different support for hardware-bound keys and attestation.
In particular, Apple silicon Macs can attest; T2 Macs cannot provide Mac
attestation. Profile and DDM ACME credential availability also differ. Consult
the [Apple ACME payload](https://developer.apple.com/documentation/devicemanagement/acmecertificate),
[Apple DDM ACME credential](https://developer.apple.com/documentation/devicemanagement/acmecredential),
and the project's [Mac capability guidance](../operations/enrollment-security.md#macos-acme-credentials)
before requesting a key. Initial software-key ACME enrollment requires the
explicit `DM_ACME_ALLOW_UNATTESTED` policy opt-in. Choose SCEP when it better
fits the device and your identity policy.

Leave `DM_ACME_ANCHOR_FILE` unset to use Apple attestation roots for live
devices. A simulator's attestation CA is not a live device trust anchor.
Preserve the identifier key across restarts and replicas. Incomplete issuance
recovers through authorized finalize retries or ordinary order polling using
the same persisted certificate receipt; pending receipts cannot be downloaded.

### Automated Device Enrollment

ADE requires organizational enrollment with Apple Business Manager/Apple School
Manager (Apple's current portal may use the Apple Business name), devices
assigned to your external management service, and that service's enrollment
token. The optional AxM API credential is separate from the device enrollment
service token and does not replace it. Follow Apple's
[device assignment documentation](https://developer.apple.com/documentation/devicemanagement/device-assignment)
for the Apple-side workflow.

On the `all` role, use `dmctl routes` to discover `/dep/` administration. The
sequence is: create the local account and token-encryption identity, exchange its
public certificate for the Apple service token, import the `.p7m`, configure the
enrollment profile, synchronize assigned devices, and assign/read back the desired
profile. Add the exact local account name to `depAccounts` in the admission file
and restart. Admission checks the configured profile assignment and rejects
deleted devices. The default profile URL is `DM_PUBLIC_URL` plus `/enroll/ade`.

The [reference DEP handler](../../server/internal/app/dep.go) serves these
operations through `dmctl api`; the names below are relative to `/admin/v1`:

| Method and path | Input or result |
|---|---|
| `GET /dep/accounts` | Account names, token state, configured profile, and expiry metadata |
| `PUT /dep/accounts/{name}/keypair` | Generate the token-encryption identity; returns the public certificate PEM to upload to Apple |
| `PUT /dep/accounts/{name}/token` | Import the downloaded binary `.p7m` token through `--file` |
| `PUT /dep/accounts/{name}/profile` | Submit the intended Apple device enrollment service profile JSON |
| `POST /dep/accounts/{name}/sync` | Synchronize inventory, then assign the configured profile; inspect the results |
| `GET /dep/accounts/{name}/devices` | Inspect local device and assignment state |

Preserve the keypair used for a pending token exchange. The separate raw-JSON
`/tokens` route is for development/tests; the Apple portal workflow uses `/token`.

`DM_DEP_SYNC_INTERVAL` enables background synchronization; with it unset/zero,
use the explicit API operations. Inspect per-device assignment outcomes before
beginning Setup Assistant. See the [DEP integration decision](../research/decisions/0026-dep-client-sync-and-assignment.md)
and [executable client scenario](../../server/e2e/dep_test.go) for token and
assignment behavior.
If you enable the ADE web-authentication lane, also complete OIDC setup below.
Keep signature verification enforced; `DM_ADE_AUDIT` is not an enrollment fix.

### Account-driven Device Enrollment and User Enrollment

Prepare Managed Apple Accounts and the appropriate enrollment/discovery setup
for their domain. Choose the flow supported by your target OS and organization;
Apple's [account-driven onboarding](https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment)
describes service discovery, user authentication, enrollment, and ongoing
authorization. These are device enrollment credentials, separate from the
administrative tokens created in section 6.

The reference server requires an upstream OIDC provider for its browser sign-in.
Register the exact callback `https://mdm.example.com/enroll/oidc/callback` with
that provider and configure `DM_OIDC_ISSUER`, `DM_OIDC_CLIENT_ID`, and
`DM_OIDC_CLIENT_SECRET`. Private provider trust uses `DM_OIDC_ROOT_CA_FILE`.
Configure `DM_DISCOVERY`, for example `Mac=mdm-adde,iPhone=mdm-byod`, and choose
`DM_ACCOUNT_DRIVEN_METHOD=apple-as-web` or `apple-oauth2`. These mappings select
Device Enrollment or User Enrollment; absent model families are rejected.

Publish the required `/.well-known/com.apple.remotemanagement` discovery response
where Apple's discovery process for the Managed Apple Account can find it.
Setting a variable on a different hostname does not configure your domain or
Apple account. Add exact issuer/subject or verified-email account rules to the
admission file, including the resulting `managedAppleAccount` and any required
groups; the [operations guide](../operations/enrollment-security.md#enrollment-admission-and-issuance)
shows the JSON shape.

The reference composition persists browser handoffs, token grants, and
certificate associations through shared protocol state on SQL. Replicas need
the same database, keys, issuer, and policy. Preserve Apple's platform/channel
bearer behavior: the macOS device channel omits the ongoing access token while
the user channel sends it. Do not add an admin token or extra client OAuth
requirements to that exchange.

### Declarative device management

DDM extends an existing MDM enrollment. Start with the working enrolled device
and `all` role. A first DDM workflow creates supported declarations, includes
the necessary activation references, adds them to a set, assigns that set to
the enrollment, and triggers synchronization. Check declaration/status results
from the device. A stored declaration alone is not an applied configuration.

`dmctl declarations`, `sets`, and `notify` administer this flow. Use
[E2E-008 and E2E-009](../testing/bench-catalogue.md) and the
[DDM scenarios](../../server/e2e/ddm_test.go) before choosing a real-device
configuration. `DM_DDM_SUBSCRIPTIONS` defaults to enabled. DDM credentials,
payload availability, activation predicates, and status reporting have their
own contracts; see the [architecture guide](../architecture.md#declarative-device-management).

## 10. Deploy and operate the server

### Persistent backend and process ownership

| Backend | Configuration and requirements |
|---|---|
| SQLite | `DM_STORAGE=sqlite`, `DM_DSN=/absolute/path/dm.sqlite`; writable local directory, keyring, and backups that include consistent WAL state |
| PostgreSQL | `DM_STORAGE=postgres`, `DM_DSN` in pgx format; provision the database/user, verified database transport as appropriate, schema privileges for startup migrations, and the same application keyring |
| MySQL | `DM_STORAGE=mysql`, driver DSN such as `user:password@tcp(db.example.com:3306)/dm?tls=true`; MySQL 8.0.19+ syntax is required, with database/user provisioned and keyring configured |
| Memory | `DM_STORAGE=inmem`; temporary development state with no restart durability |

The code normalizes MySQL time parsing and UTC handling. Protect DSNs as
credentials. The integration database scripts create test databases, not a
production database service. Switching `DM_STORAGE`/`DM_DSN` does not migrate
existing enrollment state. `dmctl export/import` is privileged enrollment
migration tooling, not a full backup of all stores, keys, policies, and issuers.

Run the binary under a supervisor with a dedicated service account, protected
configuration, persistent data, restart policy, and graceful termination. Native
shutdown drains HTTP before stopping workers and closing storage. The current
shutdown deadline is 10 seconds; give the supervisor enough time for that drain.
Keep host time accurate for certificates, tokens, signatures, and private-hop
freshness checks.

### Containers and split roles

Build the maintained image with `make docker-build`. It runs as non-root, has no
shell, uses `/data/dm.db`, and still defaults to a loopback listener. Publishing
a container port does not make that loopback socket reachable through the
container network. For network access, explicitly supply a non-loopback
`DM_LISTEN`, native TLS files, persistent `/data`, readable secret mounts, and
the rest of your deployment configuration.

The image's default healthcheck uses HTTP. When enabling TLS, replace it with
an HTTPS probe whose hostname matches the certificate and whose trust store
contains its CA. The binary's `-check URL` uses the process/system trust store;
it has no `-ca-file` flag for server trust. Plan the probe's trust configuration
and volume ownership before starting the container. The
[container integration script](../../scripts/testdb.sh) demonstrates a private
TLS test setup.

In a split deployment:

- `mdm` owns device traffic and forwards DDM to `DM_DDM_URL`.
- `ddm` owns the declaration engine and its administrative family.
- Administrative routes operate on the stores/components constructed by each
  process. Direct DDM administration to the engine answering device DDM
  requests, and inspect `status`/`routes` on both processes. Retain `all` for
  enrollment policies requiring DEP inventory: it wires DEP before enrollment
  construction. Later availability of a DEP admin route does not satisfy that
  startup dependency.

The private hop requires HTTPS, shared persistent state, and two independent
random keys of at least 32 bytes. The MDM process's send key equals the DDM
process's receive key; the DDM send key equals the MDM receive key. Configure
`DM_DDM_ROOT_CA_FILE` for private trust. The `ddm` role requires native TLS even
on loopback. There is no environment variable to enable its programmatic
insecure-test exception. Start with the
[split deployment scenario](../testing/bench-catalogue.md) and
[transport contract](../operations/enrollment-security.md#identity-transport-and-storage).

### Renewal, backups, and observability

Keep an operational inventory of the public hostname, HTTPS chain, issuer
certificate/key, APNs topic/certificate/key and portal account, storage keyring,
identifier key, administrator credentials, DEP tokens, OIDC credentials, and
optional service/API keys. Assign renewal owners and monitor expiry.

For the file push source, deploy the renewed matching pair and restart. With
`DM_PUSH_SOURCE=store`, use `dmctl pushcerts put --cert ... --key ...` and confirm
the stored topic/version; the store-backed clients reload committed renewals.
`pushcerts list` lists the store, so it does not prove that a file-backed source
was loaded. Preserve the enrollment topic across renewal.

Test a protected, consistent backup restore with the same keyring, issuer, and
configuration. Retain accepted old storage keys until all applicable stores
have been rewrapped. The [operations guide](../operations/enrollment-security.md)
describes encryption coverage and rotation; app-push credential rotation also
has [namespace-specific requirements](../operations/reference-bench.md#persistence-and-migration).

Replace enrollment profiles before their relevant certificates expire, following
[Apple's certificate lifecycle guidance](https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices).
Use authorized replacement instead of enabling broad changed-certificate
reenrollment. Keep `DM_ALLOW_REENROLL` and `DM_RETURN_TO_SERVICE` disabled unless
you deliberately implement their policies. Return to Service can authorize
erasure and reenrollment.

Certificate revocation is enabled by default when the reference enrollment
composition runs. Maintain its CRL/OCSP publication paths and issuer material.
Device checkout does not automatically revoke its identity. Inbound rate limits
are separately configured through `DM_RATE_LIMITS`; choose limits from measured
load and Apple's retry behavior rather than copying arbitrary quotas.

Use storage health, worker readiness, audit events, issuance failures,
persistent ACME `processing` orders, command acknowledgments, and push outcomes
as separate signals. ACME registration recovery occurs on polling/retry; there
is no background registration worker. Optional webhooks require HTTPS and
support `DM_WEBHOOK_ROOT_CA_FILE` and `DM_WEBHOOK_HMAC_KEY`. Audit records and
webhooks are projected events, not raw device-message archives.

Before broader use, run the [physical-device checklist](../wip/apple-conformant-security-hardening-2026-09-11.md#physical-device-checklist--not-executed)
for your actual OS/hardware/modes, including replacement, failure recovery, and
proxy identity. Keep results distinct from simulator results.

## 11. Use the library in your application

### Select modules and versions deliberately

The root module supplies protocol/schema types, enrollment and PKI components,
Apple service clients, interfaces, and in-memory stores. The separate `server`
module supplies reusable SQL stores, `service`, `httpapi`, authorization, and
DDM/push adapters. `server/internal/app`, `server/internal/runtime`, and
`server/internal/dmctl` are internal application wiring and cannot be imported
by an external module.

In your application's directory, outside this repository, initialize its module
if needed. Set `DM_REV` to a reviewed tag or commit containing the
`devicemanagement/` package paths used below; older releases use the previous paths:

```sh
go mod init example.com/my-device-service
export DM_REV='REPLACE-WITH-REVIEWED-TAG-OR-COMMIT'
go get "github.com/deploymenttheory/go-apple-dm@$DM_REV"
# Add this only when using public packages in the server module:
go get "github.com/deploymenttheory/go-apple-dm/server@$DM_REV"
```

The server declares the minimum published library version required by its APIs;
installing the server alone resolves that dependency. Local development uses
`go.work` rather than replacement directives in the released module. Maintainers
run `make verify-server-module-installation` to verify dependency resolution with
workspaces disabled, build the candidate server packages, and install `dmserver`
and `dmctl` into a temporary directory using that exact declared library version.
The command reports build and installation compatibility; runtime behavior has
separate test suites.

The two modules have independent module tags. For an exact source snapshot,
using the same repository commit for both avoids accidentally pairing a new
server with an older released root library. Check both selected versions in
your `go.mod`; pin and review updates because the API is pre-1.0.

For development against a local checkout, use a **separate application workspace**
that includes your application directory, this repository, and its `server/`
directory. The repository's own `go.work` applies when working in this checkout;
it does not automatically govern another application elsewhere on disk.

### Build a typed command without a running server

Save this as `main.go` in your application module. The example validates a query
for a specified Mac target and writes a complete MDM command plist to stdout.
It makes no network requests and does not queue the command.

```go
package main

import (
    "log"
    "os"

    "github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
    "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
    "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

func main() {
    query := &commands.DeviceInformation{
        Queries: []string{"OSVersion", "BuildVersion"},
    }
    target := support.Target{
        OS: support.MacOS, Version: support.V(15, 0, 0),
        Channel: support.ChannelDevice,
    }
    if err := query.Validate(target); err != nil {
        log.Fatal(err)
    }
    command, err := mdm.NewCommand(query)
    if err != nil {
        log.Fatal(err)
    }
    if _, err := os.Stdout.Write(command.Raw); err != nil {
        log.Fatal(err)
    }
}
```

```sh
go run . > inventory.plist
```

Use the device's real target metadata in an integration. `NewCommand` creates
the envelope, request type, and UUID; validation is an explicit earlier step.
Generated validation does not cover every protocol rule Apple documents in
prose. The resulting file can be supplied to the `dmctl commands send` workflow
in section 8.

### Compose only the services you need

| Need | Public packages and next example |
|---|---|
| Read/write Apple messages | `devicemanagement/mdmprotocol/mdm`, `devicemanagement/mdmprotocol/plist`, `devicemanagement/schema/commands`, `devicemanagement/schema/checkin`, `devicemanagement/schema/profiles`, `devicemanagement/schema/ddm`, `devicemanagement/schema/support` |
| Build enrollment profiles and authentication flows | `devicemanagement/mdmprotocol/enroll`, its `ade`, `accountdriven`, `discovery`, and `webauth` packages |
| Issue and validate device identities | `devicemanagement/pki/ca`, `devicemanagement/pki/scep`, `devicemanagement/pki/acme`, `devicemanagement/pki/acme/attest`, `devicemanagement/pki/revocation` |
| Persist MDM and protocol state | `storage`, `state`, memory implementations; `server/sqlstore/{sqlite,postgres,mysql}`, `server/statestore`, and the other domain stores |
| Serve MDM HTTP traffic | `server/service` and `server/httpapi`; see [the harness](../../server/e2e/harness_test.go) and [reference composition](../../server/internal/app/app.go) |
| Send MDM push notifications | `devicemanagement/appleplatformservices/push`, its `apns` implementation, and `server/pushnotify` |
| Add DDM | `devicemanagement/mdmprotocol/ddm`, `devicemanagement/storage/ddm`, `server/ddmadapter`, `server/ddmsync`; see [DDM scenarios](../../server/e2e/ddm_test.go) |
| Call Apple services | `devicemanagement/appleplatformservices/dep`, `axm`, `gdmf`; configure their separate credentials, trust, and stores |
| Test protocol integration | `simulator`, `testpki`, service fakes, and backend contract suites; never install fixture trust in a live deployment |

For an embedded MDM service, follow this construction and request sequence:

1. Open the selected domain stores and shared transactional protocol state.
   Configure `devicemanagement/storage/crypt` and a `secrets.Provider` explicitly for persistent
   secrets. Low-level SQL constructors can accept a nil keyring; the reference
   executable's requirement is not automatically applied to your application.
2. Configure trusted issuer material, admission, issuance registration, and
   certificate status. Establish the certificate's authorized device/account
   association before a device's first Authenticate. Pinning a certificate
   alone is not organizational admission.
3. Construct `service.Core` using `service.New`, the MDM store, hooks, and needed
   message handlers. Pin enforcement, denied certificate reuse, and denied
   changed-certificate reenrollment are defaults. Certificate-status checking
   is an optional callback here; reference-server revocation defaults do not
   configure an embedded service for you.
4. Construct `httpapi.Handler` with the core as its check-in/connect handlers.
   Mount certificate-verification middleware and verified HTTPS. Prove key
   possession through verified TLS, CMS, or explicitly trusted proxy evidence.
   Add enrollment delivery/authentication routes separately.
5. Wire push notification, DDM synchronization, state cleanup, and other workers
   you need, and own their context/shutdown lifecycle. Calling `service.New`
   does not start the reference executable's workers or administrative API.
6. After successful enrollment, call `Core.Enqueue` with the typed command and
   target enrollment IDs. Inspect both queued and skipped targets, request the
   appropriate wake, and await the device response. Keep device/user channels
   and parent identifiers distinct.

The reference server's `DM_*` variables configure the executable; importing a
package does not read those variables or enable that behavior. The
[architecture guide](../architecture.md) explains module boundaries. The e2e
harness contains deliberate test shortcuts and fake services; copy the relevant
contracts rather than treating its complete setup as deployment configuration.

### Preserve issuance and storage contracts

ACME signers must perform pure signing inside the serialized order transaction.
Move depot/registry/association side effects to `acme.Config.Register`, which
must be idempotent and safe under concurrent recovery calls. A receipt and CSR
hash commit before registration; only completed registration makes the
certificate downloadable. Custom `acme.Store` implementations must acquire the
cross-instance `UpdateOrder` lock before reading state and pass
`devicemanagement/storage/acme/acmetest`.

For SCEP grant issuance, provide an explicit challenge policy, shared grant
state, pure signer, and idempotent registration. `RenewalOnly` does not authorize
initial enrollment. Account-driven registration must also establish the
authenticated account association. See the
[library issuance sequence](../operations/enrollment-security.md#library-issuance-and-transport-configuration)
for retry, recovery, and callback requirements.

Custom administration stores must implement atomic `ApplyPrincipal` and pass
`server/adminauth/adminauthtest`; custom MDM and shared-state backends must also
pass their corresponding contract suites. Concurrent operations across
replicas require shared transactions, not a mutex in each application process.
Keep issuer keys, identifiers, storage keys, and policy consistent. Stop older
writers when upgrading across changes to these contracts.

### Validate your integration

From the repository root, the maintained checks include:

```sh
make verify
make test-e2e E2E_STORE=sqlite
make test-acceptance
```

For backend development, run `make testdb-up`, export the `TEST_POSTGRES_DSN`
and `TEST_MYSQL_DSN` values it prints, then run `make test-storage`. Both
databases are needed to exercise both SQL contracts; unconfigured external
backends can be skipped. `make testdb-down` removes the temporary test databases.
Use dedicated test databases because contract tests reset their data.

`make test`, `make lint`, `make fuzz-smoke`, and `make coverage` are the broader
development gates described in [CONTRIBUTING](../../CONTRIBUTING.md).
Coverage evaluates collected profiles; it is not a substitute for first running
the suites that produce them. Use physical-device tests for Apple behavior the
simulator cannot establish.

## 12. Troubleshoot by milestone

| Symptom | What to check next |
|---|---|
| Build cannot find packages or reports an older library API | Go version, current repository root, `go.work`, and both selected module revisions |
| Persistent server refuses startup | `DM_STORAGE_KEYS`, exact key filenames/environment names, readable material, writable data directory, and configured database connectivity |
| Decryption fails after restart | Restore the original key material and names; verify the correct database/configuration pair instead of regenerating keys |
| Listener reports HTTP requires loopback | Use literal `127.0.0.1`/`[::1]` for local HTTP, or configure both native TLS files for a remote listener |
| Container is unreachable | Bind to a container-network address with TLS, publish/map the correct port, and configure a matching HTTPS healthcheck |
| CLI reports untrusted certificate or hostname mismatch | Correct the HTTPS chain, DNS SAN, URL, and `-ca-file`; `localhost` is not accepted for plain HTTP |
| Admin 401 | Correct server/token source, enabled authentication mode, expiry/revocation, and whether a static token was removed during restart |
| Admin 403 | Applicable Cedar policy, root requirement for the action, or last-active-root protection; `status` alone is not a policy test |
| Principal/profile/DEP route is absent | `DM_ADMIN_STORE`, enrollment enablement, current role, and the active route table; a missing feature family is different from a missing resource |
| Server is healthy but profile issuance is denied | Explicit admission policy, exact UUID/serial or authenticated account, current assignment for DEP admission, and whether all replicas restarted after a policy edit |
| Profile cannot connect or install | Device trust before profile download, reachable public URL, device clock, profile contents, issuer validity, and OS/hardware support |
| Profile installation cannot finish SCEP/ACME | Expired admission/credential, changed issuer or identifier key, unsupported attestation request, wrong binding, or pending registration errors |
| Authenticate is rejected | Issuance provenance and certificate-to-identifier association, identity trust source, old enrollment pin, disabled state, and certificate status |
| APNs credentials pass locally but pushes fail | MDM certificate type, matching key/topic, expiry, selected push source, production connectivity, and the device's current TokenUpdate |
| Command remains queued | Explicit push request, APNs outcome, device connectivity, correct channel, and recent polling; APNs success is not command acknowledgment |
| Command returns `Skipped`, `NotNow`, or an error | Requested rights, observed target capabilities, Apple support metadata, response ErrorChain, and documented retry behavior |
| Account-driven sign-in loops | Domain discovery, OIDC callback/issuer/client configuration, account admission rules, browser cookies, shared state, and platform-specific bearer rules |
| DDM changes do not apply | Existing active enrollment, supported declarations and activation references, set membership, notifier, and device status reports |
| Bench init refuses an existing workspace | Reuse its configuration with `bench-up`, or choose a new workspace; preserve its identity/key files |

Use `dmctl -h`, `dmctl routes`, the [root configuration reference](../../README.md#reference-server),
and [enrollment security operations](../operations/enrollment-security.md) to
resolve configuration questions. Keep tokens, private keys, downloaded profiles,
raw responses, and privileged exports out of shared logs and issue reports.
