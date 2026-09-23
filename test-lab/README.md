# Reference-server lab

The lab runs the ordinary `dmserver` executable. `dmctl lab` owns workspace
setup, external-service fixtures, module selection and private evidence. The
native app in `host-app/` is a device-side fixture, not another provider server.

Run commands from the repository root. See the [module catalogue](../docs/testing/lab-catalogue.md),
[testing guide](../docs/testing/lab.md), and [design decision](../docs/research/decisions/0048-reference-server-bench.md).

## Simulated modules

```sh
make lab-init
make lab-up
```

`lab-up` stays in the foreground and supervises its children. In a second terminal:

```sh
make lab-list
make lab-status
make lab-run LAB_MODULES=E2E-006
make lab-run LAB_MODULES=all
make lab-down
```

`all` selects the modules supported by the workspace's mode. An explicit unsupported
module returns a nonzero exit status. Modules requiring another configuration
(for example ACME, user authentication, return-to-service, or enrollment admission)
start an isolated temporary instance of the same server executable and clean it up.
The catalogue records those configuration choices. They never reconfigure a live server.

The default workspace is `test-lab/local`, SQLite, all-in-one, and simulated.
`LAB_WORKSPACE`, `LAB_MODE`, `LAB_STORAGE` and `LAB_LISTEN`
configure `lab-init`. Existing workspaces are read from their private `lab.json`;
init refuses to overwrite them. PostgreSQL/MySQL workspaces require a private `DSN`
setting. Each workspace runs the unified reference server.

Simulated APNs uses mutual TLS with generated provider identities. DEP, ABM, OIDC,
and attestation fixtures are local. No Apple account or physical device is required.
Results are written beneath `local/evidence/<run>/` as `results.json`, `junit.xml`
and a self-contained `report.html`. They identify the module, target, stage, mode,
adapter, revision, outcome and duration. A failed, blocked, or explicitly
unsupported module makes the command fail. `make lab-report LAB_RUN=<dir>`
rerenders the HTML report from an existing run.

## Container stack for device testing

A live workspace can run the server in containers so a device on another network can reach
it. See [the lab stack](../deploy/lab/README.md) and the
[testing guide](../docs/testing/lab.md#server-adapters-and-the-container-stack).

```sh
make lab-init LAB_MODE=live LAB_ADAPTER=docker LAB_HOSTS=mdm.lab.test,192.168.64.1
make lab-doctor
make lab-up
```

`lab doctor` reports docker, the compose file, free space and the published addresses.
`make lab-tls LAB_HOSTS=…` reissues the HTTPS leaf from the retained CA when the lab's
address changes. `make lab-tools` builds the guestweave CLI used to drive virtual Macs.

## Live workspace and existing material

All local certificates, keys, profiles, device tokens, databases and receipts stay
under the gitignored `test-lab/local/`. Init preserves an existing complete `mdm/`
identity directory. It refuses partial identities instead of replacing them.
The live runtime reuses `mdm/mdm.sqlite` and accepts the previous `lab` storage-key
name; simulated storage uses a separate database. Keep the original storage key.

For a fresh live workspace:

```sh
make lab-init LAB_MODE=live
make lab-doctor
make lab-up
```

If a workspace already exists, stop it before changing `Mode` in its private
`lab.json` to `live`. A live workspace starts without an MDM certificate so app
pushes can be tested independently. MDM enrollment becomes available once an MDM
push certificate is installed at `mdm/push.pem` and the server is restarted.
Profiles and trust are installed manually; lab commands do not enroll the host.

## App certificate and host app

An app APNs certificate authorizes its app topic. It does not authorize MDM pushes.
The matching CSR private key is required; neither the certificate nor CSR can
reconstruct that private key. Inspect the supplied certificate without sending it:

```sh
go run ./server/cmd/dmctl apns inspect -cert /Users/dafyddwatkins/Desktop/com.weaveplatform.deviceweave.applepushservices
```

Validate the identity before uploading it:

```sh
go run ./server/cmd/dmctl apns check -kind app -cert test-lab/local/app/certificate.pem -key test-lab/local/app/push.key
```

If the original key cannot be recovered, obtain a replacement app certificate for
the same App ID using a new CSR. Existing `replacement.key` and `replacement.csr`
material must be preserved. For new output files only:

```sh
go run ./server/cmd/dmctl pushcerts csr -cn 'DeviceWeave App APNs' -key-out test-lab/local/app/new.key -csr-out test-lab/local/app/new.csr
```

Upload that CSR through Apple's app APNs certificate creation flow, then validate
the issued certificate with its matching key. Import the pair through the server:

```sh
test-lab/local/bin/dmctl -server https://localhost:8443 -ca-file test-lab/local/mdm/ca.pem -token @test-lab/local/mdm/admin-token apppush put -cert test-lab/local/app/certificate.pem -key test-lab/local/app/push.key
```

Place the macOS provisioning profile at `test-lab/local/app/embedded.provisionprofile`,
then build and run with your signing identity:

```sh
LAB_SIGN_IDENTITY='Apple Development: YOUR IDENTITY' test-lab/host-app/build.sh
test-lab/host-app/run.sh
```

For a compilation check without APNs registration, use `test-lab/host-app/build.sh --unsigned`.
The bundle ID is
`com.weaveplatform.deviceweave`. The signed entitlement determines the APNs
environment; the app exports `app/registration.json` and matching receipt files.
The provider key stays on the server.

```sh
make lab-run LAB_MODULES=APP-001
make lab-run LAB_MODULES=APP-002
```

A live app module passes only after APNs accepts the request and a receipt matches
its correlation ID, topic and environment. APNs acceptance alone is not delivery.
Renew through `apppush put`; metadata listings never return private keys.

## MDM certificate and enrollment

The MDM workflow uses Apple's [vendor CSR signing certificate process](https://developer.apple.com/help/account/certificates/mdm-vendor-csr-signing-certificate)
and a customer MDM push certificate, separately from [app APNs provisioning](https://developer.apple.com/documentation/usernotifications/establishing-a-certificate-based-connection-to-apns).
The [capability request form](https://developer.apple.com/contact/request/mdm-capability) requires an Apple Developer account sign-in. Existing private
customer and vendor key/CSR files remain usable. For fresh output paths:

```sh
go run ./server/cmd/dmctl pushcerts csr -cn 'DeviceWeave customer MDM' -key-out test-lab/local/mdm/customer-new.key -csr-out test-lab/local/mdm/customer-new.csr
go run ./server/cmd/dmctl pushcerts sign -h
```

`pushcerts sign` validates the vendor signing identity and chain and produces the
signed customer CSR envelope for Apple's Push Certificates Portal. Keep the
customer key paired with the resulting MDM certificate. Save the issued identity
as `mdm/push.pem` and `mdm/push.key`, validate it with `apns check -kind mdm`, then
restart the lab. First startup imports it through the regular admin API;
subsequent stored renewals remain authoritative.

Request a profile from the configured server, specifying the actual device UUID:

```sh
test-lab/local/bin/dmctl lab profile -device-id DEVICE-UUID -file test-lab/local/mdm/enrollment.mobileconfig
```

Review and install the profile manually. The default operator-issued profile
requests device inventory access, uses the configured SCEP/ACME issuance policy,
and sends CheckOut when removed. After Authenticate and TokenUpdate:

```sh
make lab-run LAB_MODULES=LIVE-001 LAB_DEVICE_ID=DEVICE-UUID
```

The module queues `DeviceInformation`, requests an APNs wake, and verifies the
acknowledged response contains OSVersion and BuildVersion. A timeout retains a
failed result; it does not claim successful delivery.

## Troubleshooting and cleanup

- `lab doctor` lists missing local/live material without changing the device.
- Inspect server startup output for invalid credentials, occupied ports or storage failures.
- `lab status` verifies the supervisor is reachable. `lab down` requests an
  orderly shutdown using a private control credential rather than signalling a saved PID.
- A blocked prerequisite and a device response failure have different report statuses.
- Stop the lab before editing its configuration. Preserve local identity files
  and databases when restarting. Uninstall device profiles manually after testing.
- `dmlab`, `lab.py` and the earlier `dmctl bench` command have been retired. Use the commands above for all lab orchestration.

## ACME and SCEP enrollment

The [Mac enrollment runbook](../docs/operations/mac-enrollment-testing.md) covers
preflight, local HTTPS trust, manual enrollment, authorized identity replacement
and the separate ACME/SCEP live acceptance runs. These use the maintained server
and lab; no separate enrollment spike executable is needed.

## macOS 27 preparation and handoff

Use the [feature fixtures](apple-features/README.md) and [device readiness procedure](../docs/operations/mac-enrollment-testing.md#feature-acceptance-and-vm-readiness) for native acceptance prerequisites and cleanup.
