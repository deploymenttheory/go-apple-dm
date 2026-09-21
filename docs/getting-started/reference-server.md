# Run the reference server

[Getting started](getting-started.md) · [Configuration explained](configuration.md)

This walkthrough starts a persistent HTTPS server, verifies administrative
access, then prepares a first manually enrolled Mac. Stop after step 5 if you
only want to explore the server. Apple credentials are needed in step 6.

1. [Start Compose](#1-start-the-compose-package).
2. [Check HTTPS and CLI access](#2-verify-https-and-administrative-access).
3. [Edit the JSON configuration](#3-inspect-and-edit-the-json-configuration).
4. [Manage administrator access](#4-manage-stored-administrator-access).
5. [Stop and resume](#5-stop-and-resume-without-losing-state).
6. [Enroll a Mac and retrieve inventory](#6-enroll-your-first-real-mac).

Prefer binaries on the host? Jump to [native startup](#7-run-without-docker).

Commands use a macOS/Linux shell from the repository root. Terminal output
below was captured from the built CLI and local Compose services. Excerpts are
labelled; generated IDs, timestamps and build versions can differ. The
physical-device steps describe checks you must perform on your own device.

## 1. Start the Compose package

Install Git and Docker with a current Compose plugin supporting `up --wait`,
start Docker, and check it:

```sh
docker info
docker compose version
git clone --recurse-submodules https://github.com/deploymenttheory/go-apple-dm.git
cd go-apple-dm
```

If you already have the checkout, run `git submodule update --init --recursive`.
Do not advance the Apple schema submodule: it is pinned to this revision.

Define a shortcut so the following commands all use the supplied Compose file:

```sh
dc() { docker compose -f deploy/quickstart/compose.yaml "$@"; }
dc --profile tools build
dc up -d --wait
```

The build downloads dependencies and compiles both binaries inside Docker. You
do not need Go on the host. The first build takes longer than later starts.
The package contains:

| Service | Job |
|---|---|
| `bootstrap` | One-shot setup: generates protected secrets, SQLite state, a local HTTPS CA/leaf and an enrollment issuer |
| `dmserver` | Non-root, distroless server with native HTTPS and a verified local healthcheck |
| `dmctl` | CLI container, run on demand with the tools profile; uses the same CA and token files |

The named `state` volume holds configuration, SQLite data and keys under `/data`.
The server runs MDM and DDM together and uses certificate role `customer`. Port 8443 is
published on the host's **loopback address only**.

If port 8443 is occupied, set `export QUICKSTART_PORT=18443` before `dc up`.
Use that port for subsequent host `curl` commands. This changes the Docker port
mapping; it does not rewrite the device-facing `DM_PUBLIC_URL` in JSON.

Inspect startup:

```sh
dc ps -a
dc logs bootstrap
```

The server should become `healthy`; bootstrap should finish with exit code 0.
Bootstrap's output is:

```text
Local HTTPS and enrollment issuer ready. Existing keys and configuration preserved.
Apple push credentials and device admission are still required for real enrollment.
```

If startup fails, read `dc logs bootstrap dmserver` before continuing. The
troubleshooting table at the end maps common failures to the next check.

## 2. Verify HTTPS and administrative access

Create an ignored local directory and export the **public** HTTPS CA:

```sh
umask 077
export GS_DIR="$PWD/test-lab/local/quickstart"
mkdir -p "$GS_DIR"
dc cp dmserver:/data/https-ca.pem "$GS_DIR/https-ca.pem"
curl --fail --silent --show-error --cacert "$GS_DIR/https-ca.pem" \
  https://localhost:8443/healthz
curl --fail --silent --show-error --cacert "$GS_DIR/https-ca.pem" \
  https://localhost:8443/readyz
```

Each request returns:

```text
ok
```

`healthz` checks storage. `readyz` additionally checks configured workers. These
probes do not contact Apple or prove a device can reach the server.

Exchange the one-time bootstrap secret for a stored root credential. Run this
once; `set -C` protects an existing credential file. The token is saved privately
inside the volume and is never printed to the terminal.

```sh
dc run --rm -T --entrypoint sh bootstrap -ec '
  umask 077
  set -C
  dmctl -server https://dmserver:8443 -ca-file /data/https-ca.pem \
    -token @/data/secrets/admin -output human \
    auth bootstrap operator-root > /data/operator-root-token
'
```

Root has authority to manage access but needs an explicit permit for device
operations. Define the CLI shortcut (Compose already points it at the new token)
and install this broad policy for the local lab:

```sh
dmctl() { dc run --rm -T dmctl "$@"; }
```

```sh
dmctl policies put operator-root -file - <<'CEDAR'
permit (principal == MDM::Principal::"operator-root", action, resource);
CEDAR
```

Inspect the server:

```sh
dmctl status
dmctl enrollments list
dmctl routes
```

`status` reports `Service: device-management`, policy authorization and
`Bootstrap pending: false`. Route families describe available API features.

An empty enrollment list prints just its headings:

```text
CHANNEL  ID  ENABLED  SERIAL  OS  LAST SEEN
```

This is expected. There are no devices yet. `routes` lists the administrative
routes available in this running composition. `dmctl api` addresses paths
relative to `/admin/v1`; public health routes are queried with `curl`.

Now check certificate setup:

```sh
dmctl setup status
```

The response is JSON. These fields are an excerpt, formatted for readability:

```json
{
  "role": "customer",
  "ready": false,
  "enrollmentEnabled": false,
  "issues": ["push requires a valid active certificate"]
}
```

HTTPS and the issuer already exist. The missing **Apple MDM push certificate**
is why enrollment is not ready. `setup check` would return exit code 3 at this
stage. Do not try to fix that by disabling certificate verification.

## 3. Inspect and edit the JSON configuration

Export the current document:

```sh
dc run --rm -T bootstrap config > "$GS_DIR/setup.json"
```

Open that file in your editor. It has four sections:

| Section | What to put there |
|---|---|
| `version` | The number `1` |
| `environment` | `DM_*` server settings, all as JSON strings |
| `secretFiles` | Paths to files holding credentials; paths are inside the container |
| `setup` | Certificate role and persistent identity IDs |

For your first change, set `environment.DM_ORGANIZATION` to your lab name.
Keep the database, key names and identity IDs. Apply and restart:

```sh
dc run --rm -T bootstrap apply-config < "$GS_DIR/setup.json"
dc restart dmserver
dmctl status
```

The apply command prints:

```text
Configuration saved. Restart dmserver to apply it.
```

See [the complete example and precedence rules](configuration.md#1-create-server-configuration)
before adding other settings. The helper checks local setup loading and retains
the old document on failure. Runtime startup and device-facing connectivity
still need their own checks. Native installations edit their generated setup
file directly.

## 4. Manage stored administrator access

The first root created in step 2 can administer principals, roles and policies.
Its lab policy explicitly grants all device operations. For routine use, create
a role and a narrower policy before assigning it:

```sh
dmctl roles put operators -description 'Routine device diagnostics'
dmctl policies put operators -file - <<'CEDAR'
permit (principal in MDM::Role::"operators",
        action in MDM::Action::"OperatorActions", resource);
CEDAR
dmctl principals create diagnostic-operator -roles operators
```

Save the returned credential privately. The operator can inspect devices and
submit five diagnostic commands. Erase, lock, raw responses, secrets and
administration require separate grants. Role names alone grant nothing.

Bootstrap was consumed in step 2 and cannot be reused, even after a restart.
You may remove `DM_BOOTSTRAP_TOKEN` from the setup document and restart to remove
the unused secret from process configuration. Existing credentials remain valid.
See [access control](../operations/access-control.md) for action groups, policy
validation, root recovery and upgrading an existing installation.

## 5. Stop and resume without losing state

```sh
dc down
dc up -d --wait
dmctl principals list
dmctl setup status
```

`down` removes this project's containers and network; it retains the named
volume. The same administrator and active certificate revisions should remain.
Bootstrap resumes incomplete local setup and reuses its keys. Once initialized,
missing configuration/identity material is an error to investigate or restore,
not a reason to generate replacement enrollment identities.

Do not use `down -v` for an installation you intend to keep: it removes the
volume, including the keys and device state. A retained volume is not a backup.
Use the [backup/restore workflow](../operations/recovery.md), including external
keys, before depending on this installation.

You now have a persistent local server and working administration. To exercise
simulated devices without Apple credentials, use a **separate** workspace in
the [reference bench](../testing/bench.md). It supplies modeled Apple services
and device exchanges; the Compose server does not pretend to be APNs.

## 6. Enroll your first real Mac

The shortest lab path is an authorized test Mac running this Docker host: its
profile can reach `https://localhost:8443`. This is manual, profile-based Device
Enrollment. Apple lists this method as supervising Macs; it does not exercise
ADE. See [Apple's enrollment-method comparison](https://support.apple.com/guide/deployment/dep08f54fcf6/web)
for differences between device types and methods.

For a different device/host, first provision a stable device-facing DNS name,
HTTPS trust and ingress. `localhost` refers to that device, not to your server.
Change `DM_PUBLIC_URL`, issue a matching HTTPS identity and expose the intended
listener through your deployment. The quickstart only publishes loopback.
Follow [HTTPS provisioning](../operations/certificate-lifecycle.md#https-and-the-enrollment-issuer)
and [identity transport](../operations/enrollment-security.md#identity-transport-and-storage).
Retain `dmserver` in the HTTPS SANs if the CLI container will keep using that
hostname, or configure its server/CA for the replacement endpoint.

### 6.1 Record the device and trust the lab HTTPS CA

On the Mac, obtain its hardware UUID and serial number from System Information
or inspect:

```sh
ioreg -rd1 -c IOPlatformExpertDevice
```

Record the actual hardware UUID as `DEVICE_ID` below. It is not the serial
number. Confirm you are authorized to replace any existing management
relationship before installing the new enrollment profile.

Export the lab trust profile, then copy it to the host:

```sh
dmctl setup profile trust -out /data/https-trust.mobileconfig
dc cp dmserver:/data/https-trust.mobileconfig "$GS_DIR/https-trust.mobileconfig"
```

Install it on the test Mac using the supported profile installation UI and
approve trust as required by its OS. Confirm the intended certificate before
trusting it. An exported profile is not an installed profile. With public-CA
HTTPS, use the trust arrangement for that certificate instead.

### 6.2 Obtain the Apple MDM push certificate

The customer needs a vendor-signed CSR and access to the Apple Push Certificates
Portal. Your MDM vendor must have the applicable
[Apple signing identity](https://developer.apple.com/help/account/certificates/mdm-vendor-csr-signing-certificate/). If you
operate that vendor yourself, follow the separate
[vendor setup](../operations/certificate-lifecycle.md#apple-vendor-signing-identity).
The quickstart's `customer` role does not supply an Apple vendor certificate.

Create the customer's push request using the Apple account that will own
renewals:

```sh
dmctl setup push request -cn 'My MDM lab' -organization 'My organization' \
  -account REPLACE_WITH_APPLE_ACCOUNT
dmctl setup workflow export -id push -artifact csr -out /data/customer.csr.pem
dc cp dmserver:/data/customer.csr.pem "$GS_DIR/customer.csr.pem"
```

The request response includes `identity.pending`. For this first request it
is `"1"`; use the actual pending revision if you are resuming a workflow.
Send **only the public CSR** to your vendor. Have the vendor return the signed
request as described in the [offline signing exchange](../operations/certificate-lifecycle.md#customer-push-identity).
The private customer key remains encrypted in your database.

Use this helper to put input files into the volume as its non-root owner. It
refuses to overwrite a file; choose a new name for a later renewal:

```sh
putfile() {
  dc run --rm -T --entrypoint sh bootstrap -ec \
    'umask 077; set -C; cat > "/data/$1"' sh "$1"
}
putfile customer.signed.csr < "$GS_DIR/customer.signed.csr"
dmctl setup push sign -revision 1 -signed-request /data/customer.signed.csr
```

Upload that signed request to the
[Apple Push Certificates Portal](https://identity.apple.com/pushcert/), then
save the downloaded MDM push certificate as `$GS_DIR/push.pem`:

```sh
putfile push.pem < "$GS_DIR/push.pem"
dmctl setup push import -revision 1 -cert /data/push.pem
dmctl setup push activate -revision 1
```

Keep the owning Apple account and certificate renewal record. Renew the
existing portal certificate to preserve its APNs topic. An ordinary APNs app
certificate is not a substitute for this MDM push identity.

### 6.3 Admit exactly the test device

Create `$GS_DIR/admission.json`, replacing both placeholders:

```json
{
  "devices": [{
    "serial": "REPLACE_WITH_SERIAL",
    "udid": "REPLACE_WITH_HARDWARE_UUID"
  }]
}
```

Both nonempty identifiers must match. An empty or absent policy admits nobody.
Copy it into the volume:

```sh
putfile admission.json < "$GS_DIR/admission.json"
dc run --rm -T bootstrap config > "$GS_DIR/setup.json"
```

In the exported config, set these entries in `environment` (merge them into
the existing object; keep the other settings):

```json
{
  "DM_ENROLLMENT_POLICY_FILE": "/data/admission.json",
  "DM_IDENTITY": "scep",
  "DM_PUBLIC_URL": "https://localhost:8443"
}
```

This first example explicitly chooses SCEP. ACME is another supported identity
path with its own hardware, attestation and policy requirements; follow the
[ACME guide](../operations/enrollment-security.md#macos-acme-credentials) when
choosing it. If you changed the host port or hostname, use the actual URL here.

Apply the config and restart once to enable enrollment after initial push setup:

```sh
dc run --rm -T bootstrap apply-config < "$GS_DIR/setup.json"
dc restart dmserver
dmctl setup check
dmctl setup status
```

Continue only when the remote response has `ready: true`, no setup issues and
`enrollmentEnabled: true`. This still does not prove device reachability or APNs
delivery. For a remote deployment, verify its URL and trust from the device's
network before requesting the profile.

### 6.4 Generate and install the enrollment profile

Set the real device values in this terminal, then export the profile:

```sh
DEVICE_ID=REPLACE_WITH_HARDWARE_UUID
DEVICE_SERIAL=REPLACE_WITH_SERIAL
dmctl setup profile export -device-id "$DEVICE_ID" -serial "$DEVICE_SERIAL" \
  -identity scep -access-rights 19 -out /data/enrollment.mobileconfig
dc cp dmserver:/data/enrollment.mobileconfig "$GS_DIR/enrollment.mobileconfig"
```

Rights mask 19 allows inventory and profile inspection/installation/removal,
including later authorized profile replacement. Use 16 for inventory only if
that is all you intend to permit. Review the organization, URLs, topic, device
binding and rights before installation. The profile is unsigned; profile signing
and HTTPS trust are separate checks.

Open the profile on the authorized Mac and complete its profile-installation
flow in System Settings. OS labels vary. The CLI generates the profile; it does
not install it for you. Then inspect:

```sh
dmctl enrollments list
dmctl enrollments get device "$DEVICE_ID"
dmctl api GET "/enrollments/device/$DEVICE_ID/enrollment-evidence"
```

Look for issued identity evidence, Authenticate/certificate association and
TokenUpdate. TokenUpdate supplies the push token and push magic. Merely exporting
the profile does not establish enrollment.

### 6.5 Send one inventory command and read its result

Save this as `$GS_DIR/inventory.plist`. Use this UUID for the first request only;
new commands need new UUIDs, while a retry of this command retains its UUID:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>CommandUUID</key><string>143441B0-345E-4E40-8C10-F68760FA2E21</string>
  <key>Command</key><dict>
    <key>RequestType</key><string>DeviceInformation</string>
    <key>Queries</key><array><string>OSVersion</string><string>BuildVersion</string></array>
  </dict>
</dict></plist>
```

Queue it, inspect `Queued`/`Skipped`, then wake the device explicitly:

```sh
dmctl commands send device "$DEVICE_ID" -file - < "$GS_DIR/inventory.plist"
dmctl push device "$DEVICE_ID"
dmctl commands list device "$DEVICE_ID"
dmctl api GET "/enrollments/device/$DEVICE_ID/commands/143441B0-345E-4E40-8C10-F68760FA2E21/result"
```

This admin enqueue operation does not itself send a push. APNs acceptance is
not command completion. The result route returns 204 while no result exists,
404 for an absent command, or JSON containing `Status`, base64-encoded plist
`Response` and any `ErrorChain`. Your checkpoint is **Acknowledged** with the
requested `OSVersion` and `BuildVersion` values. Inspect `NotNow`, errors and
skipped targets before retrying.

The [Mac testing runbook](../operations/mac-enrollment-testing.md) continues
with ACME, replacement and renewal checks. Record the actual OS/hardware/mode
you validated. Stop the server only after you have finished managing the test
device; stopping containers does not remove its management profile.

## 7. Run without Docker

Use [verified release downloads](../operations/server-releases.md) matching the
documentation revision, or install Go matching `go.mod` and build:

```sh
mkdir -p bin
go build -o bin/dmctl ./server/cmd/dmctl
go build -o bin/dmserver ./server/cmd/dmserver
./bin/dmctl -h
```

`dmctl` begins its help with:

```text
dmctl administers a go-apple-dm reference server.

Usage:
  dmctl [flags] <command> [flags] [arguments]
```

Use a fresh directory and terminal without unrelated `DM_*` overrides:

```sh
./bin/dmctl setup init -dir test-lab/local/native -role customer \
  -public-url https://localhost:8443 -listen 127.0.0.1:8443
export DM_SETUP_FILE="$PWD/test-lab/local/native/setup.json"
./bin/dmctl setup https lab -cn 'Local MDM HTTPS' -hosts localhost,127.0.0.1
./bin/dmctl setup https activate -revision 1
./bin/dmctl setup issuer create -cn 'Local MDM enrollment CA'
./bin/dmctl setup issuer activate -revision 1
./bin/dmctl setup workflow export -id https-ca -artifact certificate \
  -out test-lab/local/native/https-ca.pem
./bin/dmserver -setup-file "$DM_SETUP_FILE"
```

Use each operation's actual pending revision when resuming existing setup.
The server stays in the foreground. In a second terminal at the repository root:

```sh
./bin/dmctl -server https://localhost:8443 \
  -token "@$PWD/test-lab/local/native/secrets/admin" \
  -ca-file test-lab/local/native/https-ca.pem -output human \
  auth bootstrap operator-root > test-lab/local/native/operator-root-token
```

Use `@test-lab/local/native/operator-root-token` for subsequent commands. Install
an explicit operational policy as in step 2, then follow step 4 for routine roles.
The principal store is always enabled. Native setup does not enable an optional
audit sink automatically; SQL event capture remains persistent.
Use [CLI contexts](configuration.md#5-configure-a-native-administrative-cli) to
save the server and credential reference. **Ctrl-C** drains the foreground server;
run it again with the same setup file to resume.

## 8. Choose the next capability

| Goal | Next sequence |
|---|---|
| DDM | Working enrollment → supported declarations and activations → set membership/assignment → notify → device status; use the [bench catalogue](../testing/bench-catalogue.md) and [status guide](../operations/status-and-profile-inspection.md) |
| ADE | Organizational device assignment → enrollment-service token → profile configuration/sync → admission → Setup Assistant; see [DEP design](../research/decisions/0026-dep-client-sync-and-assignment.md) |
| Account-driven enrollment | Managed account/domain discovery → OIDC registration → account admission → profile and ongoing authorization; see [enrollment operations](../operations/enrollment-security.md) |
| Production operation | Device-facing DNS/TLS → protected persistent storage and keys → scoped admins → tested backup/restore → renewal monitoring and live-device checks |
| Simulated regression tests | Separate bench workspace → doctor → up → selected scenarios → retained evidence; see [testing](../testing/bench.md) |

## Troubleshoot the checkpoint that failed

| Symptom | Next check |
|---|---|
| Docker cannot connect | Start Docker and confirm `docker info` works |
| Host port already allocated | Set `QUICKSTART_PORT` before `dc up`; use that host port in curl and the eventual public URL |
| Bootstrap exits nonzero | Read its logs; verify retained config/keys. Restore missing initialized state rather than deleting the volume |
| HTTPS certificate error | Use the exported CA and a hostname in the certificate SANs; never use an insecure bypass |
| CLI connection refused after restart | Read server logs and wait for health; a rejected runtime setting can stop startup |
| CLI unauthorized after admin handoff | Use `/data/operator-root-token`; the bootstrap secret cannot authenticate ordinary requests |
| Authenticated but forbidden | Inspect the principal's roles and Cedar permit; root flag or a role name alone does not grant ordinary actions |
| `setup check` says push missing | Complete the vendor-signed CSR and Apple portal import/activation |
| Profile generation denied | Match the actual UDID/serial to admission policy and restart after policy/config edits |
| Profile installed, no check-in | Verify device-side HTTPS reachability/trust, identity issuance and server logs |
| Command remains queued | Check TokenUpdate, push credentials/topic, explicit wake and device connectivity |
| Command skipped or returns `NotNow` | Inspect target support and response details; do not replace a stable retry with duplicate commands |

Reopen this guide's shell shortcuts and `GS_DIR` when starting a new terminal.
They are shell conveniences, not persistent server settings.
