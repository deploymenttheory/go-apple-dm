# go-apple-dm

[![Release](https://img.shields.io/github/v/release/deploymenttheory/go-apple-dm)](https://github.com/deploymenttheory/go-apple-dm/releases)
[![CI](https://github.com/deploymenttheory/go-apple-dm/actions/workflows/go-test.yml/badge.svg)](https://github.com/deploymenttheory/go-apple-dm/actions/workflows/go-test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/deploymenttheory/go-apple-dm.svg)](https://pkg.go.dev/github.com/deploymenttheory/go-apple-dm)
[![Go Version](https://img.shields.io/github/go-mod/go-version/deploymenttheory/go-apple-dm)](https://go.dev/)
[![License](https://img.shields.io/github/license/deploymenttheory/go-apple-dm)](LICENSE)
![Status: Preview](https://img.shields.io/badge/status-preview-58A6FF)

Go packages for Apple's MDM protocol, declarative device management (DDM), enrollment,
certificate issuance and Apple service clients. The repository also contains a reference
server and an admin CLI. It does not provide an inventory UI or a fleet management product.

The root module groups protocol libraries, generated schema types, storage contracts and
in-memory implementations under [devicemanagement/](devicemanagement/). The `server` module adds SQL stores, the service layer, HTTP
adapters and application wiring. Both modules require Go 1.27. The API is pre-1.0 and may
change between minor versions.

The generated API combines Apple’s OS 27 seed with a pinned historical release,
preserving management of older devices. Availability checks use each device’s OS,
version, channel and enrollment context.

## Quick start

Read the [getting-started guide](docs/getting-started/getting-started.md) for a
complete walkthrough of prerequisites, local simulation, persistent storage,
administrator setup, real-device enrollment, and library integration.

Install the library in your own module. Select a reviewed tag or commit containing
the `devicemanagement/` packages; older releases use the previous import paths:

```sh
export DM_REV='REPLACE-WITH-REVIEWED-TAG-OR-COMMIT'
go get "github.com/deploymenttheory/go-apple-dm@$DM_REV"
```

Applications using the service layer or SQL backends also import the server module:

```sh
go get "github.com/deploymenttheory/go-apple-dm/server@$DM_REV"
```

From a repository checkout, run a local development server:

```sh
DM_ROLE=all DM_STORAGE=inmem DM_ADMIN_TOKEN=dev-token go run ./server/cmd/dmserver
```

In another terminal:

```sh
curl http://127.0.0.1:8080/healthz
go run ./server/cmd/dmctl -server http://127.0.0.1:8080 -token dev-token status
```

This configuration loses state on restart. Device enrollment additionally requires a public
HTTPS endpoint, a matching push topic and certificate, and a configured enrollment identity
issuer. Read [enrollment security operations](docs/operations/enrollment-security.md) before
enabling enrollment. Behind a TLS proxy, restrict certificate-header trust to that proxy.
The listener defaults to `127.0.0.1:8080`. Remote listeners require
`DM_TLS_CERT_FILE` and `DM_TLS_KEY_FILE`; this also applies to container networks.
Remote `dmctl` connections require HTTPS with verified trust. Use `-ca-file`
or `DMCTL_CA_FILE` for a private CA. The former `-insecure` flag is rejected.

`DM_ADMIN_TOKEN` grants unrestricted administrative access and bypasses policy. To use scoped
credentials, enable the principal store, create principals and policies, then remove the
bootstrap token and restart. `dmctl status` reports accepted authorization modes.
Principal and credential mutations require root, including token rotation.
Scoped principals retain their policy-authorized device operations and reads.

The schema can also be inspected offline:

```sh
go run ./server/cmd/dmctl explain DeviceInformation
go run ./server/cmd/dmctl explain DeviceInformation -target macos:15.0,supervised
go run ./server/cmd/dmctl explain com.apple.configuration.softwareupdate.enforcement.specific
```

For embedding examples, see the package documentation and the executable scenarios in
[server/e2e](server/e2e/) and [simulator](devicemanagement/simulator/).

The [reference-server bench](test-lab/README.md) combines simulated scenarios, process acceptance, and live-device testing. It covers certificate inspection,
app alert/background pushes, MDM vendor CSR signing, and testing both paths on a Mac.
Real credentials and test evidence stay in the gitignored `test-lab/local/` directory.
Use `make bench-init`, `make bench-up`, and `make bench-run`; see the [testing guide](docs/testing/bench.md) and [API/configuration additions](docs/operations/reference-bench.md).

## Architecture

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/diagrams/system-architecture.dark.png">
  <img alt="go-apple-dm modules and protocol services" src="docs/diagrams/system-architecture.light.png">
</picture>

[Open the interactive architecture diagram](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/main/docs/diagrams/system-architecture.html).

The [architecture guide](docs/architecture.md) describes implemented capabilities, module
boundaries and limitations. The [31 interactive diagrams](docs/diagrams/README.md) show
component relationships, protocol exchanges and lifecycle transitions.

## Capabilities

- Generated MDM commands, responses, check-in messages, profiles, declarations and status
  types from a pinned Apple schema, with validation and platform support metadata.
- Enrollment lifecycle, certificate pinning, command queues, hooks and typed events.
  Account-driven enrollment associates authenticated accounts with issued certificates.
- Profile-based enrollment, Automated Device Enrollment, account-driven Device Enrollment
  and account-driven User Enrollment, with user-channel and Shared iPad handling.
- SCEP and ACME identity issuance, Managed Device Attestation verification, and optional
  certificate revocation services. Hardware support and trust policy require configuration.
- Declarative device management with versioned declaration snapshots, sets, membership,
  status reports, subscriptions and synchronization notifications.
- APNs, device enrollment service (`dep`), software lookup (`gdmf`) and Apple Business Manager
  and Apple School Manager API (`axm`) clients, with test servers.
- In-memory, SQLite, PostgreSQL and MySQL persistence; column sealing and key rotation for
  selected secrets; optional shared security state and inbound rate limits.
- A device simulator, an admin API with scoped credentials and Cedar policies, and projected
  event sinks and audit records.
- An opt-in [content-cache metrics library](devicemanagement/contentcache/) based on
  Apple's OS 27 seed OpenAPI, with an embeddable receiver and caller-owned authentication
  and storage. Consumers mount the receiver and provide authorization and acceptance callbacks.

Simulator tests verify the modeled protocol exchanges. Physical-device interoperability,
hardware attestation and deployment-specific trust configuration require separate validation.

## Reference server

`server/cmd/dmserver` supports `all`, `mdm` and `ddm` roles. `all` runs the device service and
DDM engine together. `mdm` serves device traffic and can forward DDM requests to the `ddm`
role through `DM_DDM_URL`. The private proxy protocol requires request and response HMAC keys;
requires HTTPS and shared replay protection. It is specific to this project's adapters.

Configuration is read from `DM_*` environment variables. This table groups the main settings;
[server/internal/app/env.go](server/internal/app/env.go) defines their parsing and defaults.

| Variables | Purpose |
|---|---|
| `DM_ROLE`, `DM_LISTEN`, `DM_STORAGE`, `DM_DSN` | Role, listen address (default `127.0.0.1:8080`), backend (`sqlite`, `postgres`, `mysql`, `inmem`), and DSN |
| `DM_TLS_CERT_FILE`, `DM_TLS_KEY_FILE` | Server TLS certificate and private key; required for a non-loopback listener |
| `DM_ADMIN_STORE` | Open the admin principal and Cedar policy store on this process's database, so `dmctl principals` and `dmctl policies` work. Disabled by default; enables principal and policy management routes |
| `DM_ADMIN_TOKEN` | Break-glass bearer token for `/admin/v1/`. Authenticates as root and **bypasses policy**, has no expiry, and cannot be revoked without a restart. It exists because an empty principal store authenticates nobody: set it to create the first principals, then unset it and restart. Its use is audited under the actor `break-glass`, and `dmctl status` reports whether it is still accepted |
| `DM_STORAGE_KEYS`, `DM_STORAGE_KEY_<NAME>`, `DM_SECRETS_DIR`, `DM_STORAGE_KEYS_STRICT` | Keys sealing the secret columns of a persistent store: escrow and private keys, raw check-ins and command/results, protocol state and credential-bearing declarations. `DM_STORAGE_KEYS` lists key names active-first, and the material comes from `DM_STORAGE_KEY_<NAME>` or from files in `DM_SECRETS_DIR`. A rotation prepends a name and runs `Rewrap`; `DM_STORAGE_KEYS_STRICT` then refuses any row still in clear. A persistent backend will not start without this |
| `DM_ALLOW_REENROLL` | Permit a changed certificate during `Authenticate`. Disabled by default in both the reusable service and reference server. Enable only with an enrollment authorization policy that permits the replacement; certificate chain validation alone does not bind a certificate to an enrollment identifier |
| `DM_DDM_URL`, `DM_DDM_SEND_KEY`, `DM_DDM_RECV_KEY`, `DM_DDM_ROOT_CA_FILE`, `DM_DDM_SUBSCRIPTIONS` | The split-deployment hop and synthesized status subscriptions. Independent random keys of at least 32 bytes and HTTPS are required: the hop carries a check-in verbatim and the receiving role trusts the enrollment id in that body |
| `DM_CA_FILE`, `DM_CERT_HEADER`, `DM_TRUSTED_PROXIES` | Verified direct mTLS/CMS or a trusted socket peer forwarding one validated certificate; protect the backend with TLS or loopback |
| `DM_PUBLIC_URL`, `DM_PUSH_TOPIC` | Turn on the enrollment routes; the server URL devices are given and the push topic |
| `DM_ENROLL_CA_CERT_FILE`, `DM_ENROLL_CA_KEY_FILE`, `DM_ENROLLMENT_POLICY_FILE` | Persistent CA material and explicit device/account admission policy. Empty policy denies issuance. SCEP profiles carry expiring random credentials bound to one CSR. Only memory storage permits an ephemeral development CA |
| `DM_IDENTITY` | Where an enrolled device's identity comes from: `scep` (the default) or `acme` |
| `DM_ACME_POLICY`, `DM_ACME_KEY`, `DM_ACME_HMAC_KEY`, `DM_ACME_ANCHOR_FILE`, `DM_ACME_ALLOW_UNATTESTED`, `DM_ACME_IDENTIFIER_TTL` | Which devices may enroll (`any`, `dep`, `sip`), the key the device generates (`ec256`, `ec384`, `rsa2048`, `rsa4096`), the key that mints client identifiers, extra attestation anchors for a lab, whether a device that cannot attest may enroll, and how long a client identifier stays usable |
| `DM_PROFILE_IDENTIFIER`, `DM_ORGANIZATION` | Enrollment profile identity |
| `DM_DISCOVERY`, `DM_ACCOUNT_DRIVEN_METHOD` | Service discovery per user type (`Mac=mdm-adde,iPhone=mdm-byod`) and the account-driven flow (`apple-as-web` or `apple-oauth2`) |
| `DM_OIDC_ISSUER`, `DM_OIDC_CLIENT_ID`, `DM_OIDC_CLIENT_SECRET` | The identity provider behind the ADE web view and account-driven pages |
| `DM_ADE_ANCHOR_FILE`, `DM_ADE_AUDIT`, `DM_REQUIRE_USER_AUTH` | Extra `MachineInfo` signing anchors, audit-only signature policy, and the requirement for a stored `UserAuthenticate` token on eligible user-channel `TokenUpdate` requests |
| `DM_RETURN_TO_SERVICE` | Enable the `ReturnToService` response that authorizes erasure and re-enrollment. Disabled by default; device eligibility follows Apple's protocol requirements |
| `DM_AXM_CLIENT_ID`, `DM_AXM_KEY_ID`, `DM_AXM_KEY_FILE`, `DM_AXM_SCOPE`, `DM_AXM_BASE_URL`, `DM_AXM_TOKEN_URL` | Apple Business Manager API credentials; enables `/admin/v1/axm/` |
| `DM_AUDIT_STORE`, `DM_AUDIT_RETENTION` | Persist projected events to the audit trail on this process's database, and how long to keep records (unset keeps them forever). Read it at `GET /admin/v1/audit` or with `dmctl audit list --since 1h` |
| `DM_AUDIT_LOG`, `DM_WEBHOOK_URL`, `DM_WEBHOOK_HMAC_KEY`, `DM_WEBHOOK_ROOT_CA_FILE` | Event sinks: projected slog records and an HTTPS-only MicroMDM-compatible webhook with optional SHA-256 body signatures. Both off by default. A PEM root bundle configures private webhook trust. The envelope omits `raw_payload`, which can contain a device unlock token |
| `DM_DEP_BASE_URL`, `DM_DEP_SYNC_INTERVAL`, `DM_DEP_ASSIGN_INTERVAL`, `DM_DEP_PROFILE_URL`, `DM_DEP_USE_PUT` | Device enrollment service endpoint, the background sync worker, and the DEP profile URL (defaults to this server) |
| `DM_PUSH_SOURCE`, `DM_PUSH_CERT_FILE`, `DM_PUSH_KEY_FILE`, `DM_PUSH_HOST`, `DM_PUSH_COALESCE`, `DM_PUSH_CERT_TTL` | Where APNs credentials come from and how pushes are shaped: `off`, `file` (the PEM pair, selected implicitly when a certificate file is configured) or `store` (the push certificate store). The APNs topic is derived from the push certificate; `DM_PUSH_TOPIC` separately configures the enrollment profile topic; `DM_PUSH_HOST` overrides the APNs endpoint for a lab, `DM_PUSH_COALESCE` is the window repeated pushes collapse into (negative disables it), and `DM_PUSH_CERT_TTL` how long a store-backed certificate is cached before its version is rechecked |
| `DM_PKI_REVOCATION`, `DM_RATE_LIMITS` | Certificate revocation is enabled by default; inbound rate limiting requires configuration. See [enrollment security operations](docs/operations/enrollment-security.md) for their configuration and operational requirements. |

`server/cmd/dmctl` provides typed commands for enrollments, queued commands, push certificates,
declarations, sets, notifications, principals, policies and audit records. `dmctl routes`
lists the active server routes; `dmctl api <METHOD> <path>` accesses routes without a typed
command. Availability depends on the role and enabled services.

## Development

```sh
git submodule update --init
make verify       # deterministic schema regeneration and exported-name guard
make test         # both modules, race detector and coverage
make testdb-up     # PostgreSQL and MySQL integration databases in Docker
make test-storage # requires the TEST_*_DSN values printed by testdb-up
make test-e2e     # simulator scenarios
make fuzz-smoke
make coverage     # checks the collected coverage profiles
```

Use `make help` for target details. The coverage floor is 95% overall and per non-exempt
package. See [CONTRIBUTING.md](CONTRIBUTING.md), the [test scenarios](docs/testing/e2e-scenarios.md)
and the [threat model](docs/security/threat-model.md).

## Sources

Apple's [Device Management documentation](https://developer.apple.com/documentation/devicemanagement)
and the pinned [device-management schema](third_party/device-management/) define the protocol.
The [reference catalogue](docs/research/reference_projects.md) records additional sources.
[Design decisions](docs/research/decisions/README.md) explain this project's implementation choices.

## License

MIT. See [LICENSE](LICENSE).
