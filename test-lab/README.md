# Reference-server bench

The bench runs the ordinary `dmserver` executable. `dmctl bench` owns workspace
setup, external-service fixtures, scenario selection and private evidence. The
native app in `host-app/` is a device-side fixture, not another provider server.

Run commands from the repository root. See the [scenario catalogue](../docs/testing/bench-catalogue.md),
[testing guide](../docs/testing/bench.md), and [design decision](../docs/research/decisions/0048-reference-server-bench.md).

## Simulated scenarios

```sh
make bench-init
make bench-up
```

`bench-up` stays in the foreground and supervises its children. In a second terminal:

```sh
make bench-list
make bench-status
make bench-run BENCH_SCENARIO=E2E-006
make bench-run BENCH_SCENARIO=all
make bench-down
```

`all` selects scenarios supported by the workspace's mode. An explicit unsupported
scenario returns a nonzero exit status. Scenarios requiring another configuration
(for example ACME, user authentication, return-to-service, or split deployment)
start an isolated temporary instance of the same server executable and clean it up.
The catalogue records those configuration choices. They never reconfigure a live server.

The default workspace is `test-lab/local`, SQLite, all-in-one, and simulated.
`BENCH_WORKSPACE`, `BENCH_MODE`, `BENCH_STORAGE`, `BENCH_TOPOLOGY` and `BENCH_LISTEN`
configure `bench-init`. Existing workspaces are read from their private `bench.json`;
init refuses to overwrite them. PostgreSQL/MySQL workspaces require a private `DSN`
setting. Split deployments require shared persistent storage.

Simulated APNs uses mutual TLS with generated provider identities. DEP, ABM, OIDC,
and attestation fixtures are local. No Apple account or physical device is required.
Results are written beneath `local/evidence/<run>/` as JSON and JUnit. They identify
the scenario, mode, adapter, revision, outcome and duration. A failed, blocked, or
explicitly unsupported scenario makes the command fail.

## Live workspace and existing material

All local certificates, keys, profiles, device tokens, databases and receipts stay
under the gitignored `test-lab/local/`. Init preserves an existing complete `mdm/`
identity directory. It refuses partial identities instead of replacing them.
The live runtime reuses `mdm/mdm.sqlite` and accepts the previous `lab` storage-key
name; simulated storage uses a separate database. Keep the original storage key.

For a fresh live workspace:

```sh
make bench-init BENCH_MODE=live
make bench-doctor
make bench-up
```

If a workspace already exists, stop it before changing `Mode` in its private
`bench.json` to `live`. A live workspace starts without an MDM certificate so app
pushes can be tested independently. MDM enrollment becomes available once an MDM
push certificate is installed at `mdm/push.pem` and the server is restarted.
Profiles and trust are installed manually; bench commands do not enroll the host.

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
make bench-run BENCH_SCENARIO=APP-001
make bench-run BENCH_SCENARIO=APP-002
```

A live app scenario passes only after APNs accepts the request and a receipt matches
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
restart the bench. First startup imports it through the regular admin API;
subsequent stored renewals remain authoritative.

Request a profile from the configured server, specifying the actual device UUID:

```sh
test-lab/local/bin/dmctl bench profile -device-id DEVICE-UUID -file test-lab/local/mdm/enrollment.mobileconfig
```

Review and install the profile manually. The default operator-issued profile
requests device inventory access, uses the configured SCEP/ACME issuance policy,
and sends CheckOut when removed. After Authenticate and TokenUpdate:

```sh
make bench-run BENCH_SCENARIO=LIVE-001 BENCH_DEVICE_ID=DEVICE-UUID
```

The scenario queues `DeviceInformation`, requests an APNs wake, and verifies the
acknowledged response contains OSVersion and BuildVersion. A timeout retains a
failed result; it does not claim successful delivery.

## Troubleshooting and cleanup

- `bench doctor` lists missing local/live material without changing the device.
- Inspect server startup output for invalid credentials, occupied ports or storage failures.
- `bench status` verifies the supervisor is reachable. `bench down` requests an
  orderly shutdown using a private control credential rather than signalling a saved PID.
- A blocked prerequisite and a device response failure have different report statuses.
- Stop the bench before editing its configuration. Preserve local identity files
  and databases when restarting. Uninstall device profiles manually after testing.
- `dmlab` and `lab.py` have been retired. Use the commands above for all lab orchestration.
