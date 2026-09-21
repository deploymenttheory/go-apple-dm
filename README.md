# go-apple-dm

[![Release](https://img.shields.io/github/v/release/deploymenttheory/go-apple-dm)](https://github.com/deploymenttheory/go-apple-dm/releases)
[![CI](https://github.com/deploymenttheory/go-apple-dm/actions/workflows/go-test.yml/badge.svg)](https://github.com/deploymenttheory/go-apple-dm/actions/workflows/go-test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/deploymenttheory/go-apple-dm.svg)](https://pkg.go.dev/github.com/deploymenttheory/go-apple-dm)
[![Go Version](https://img.shields.io/github/go-mod/go-version/deploymenttheory/go-apple-dm)](https://go.dev/)
[![License](https://img.shields.io/github/license/deploymenttheory/go-apple-dm)](LICENSE)
![Status: Alpha](https://img.shields.io/badge/status-alpha-58A6FF)

Go packages for Apple's MDM protocol, declarative device management (DDM), enrollment,
certificate issuance and Apple service clients. A reference server and the `dmctl` CLI show
one way to compose them, and a device simulator exercises the modeled protocol exchanges.
The project is a library to build on, not a device management product: it ships no
management UI, and the reference server exists to demonstrate what the library supports.

## Why

Building an Apple device management product or internal tool means implementing check-in
and command delivery, several enrollment modes, certificate issuance, push notifications,
declarative management and Apple's service APIs, all against a schema that Apple revises
with each OS release. Most of that work is the same for every product, and it has to be
finished before the part that differentiates a product can start.

The library provides reusable protocol, enrollment, certificate and Apple-service
packages behind storage interfaces. Types, validation and platform support metadata
come from Apple's [Device Management Client Schema](https://github.com/apple/device-management)
and the pinned compatibility input. The reference server composes these packages
with persistent SQL state, managed administration and background workers.

Apple defines the [MDM exchanges](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device)
and [DDM integration](https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management).
The project's [architecture](docs/architecture.md) and
[decisions](docs/research/decisions/README.md) explain implementation choices and
operator responsibilities.

## Who it is for

- Go developers building a device management product or internal tool, who want to import
  protocol, enrollment, PKI and DDM packages and keep their own data model, policy and UI.
- Mac admins who want to run the reference server for a fleet, or assemble their own server
  from the library on a supported SQL backend.
- Engineers studying how Apple device management works. The decision records, simulator
  scenarios and diagrams describe the protocol exchanges as implemented.
- Individuals managing their own or their family's Apple devices, who want to put a small
  UI of their own on tested foundations. Enrolling real devices still requires an Apple MDM
  push certificate, trusted public HTTPS and an enrollment identity issuer; the reference
  server and simulator run without them. See the
  [getting-started guide](docs/getting-started/getting-started.md).

## Scope

Two consumers appear in the last column: the product developer, who builds on the library
or the reference server, and the operator, who runs the result. One person or team is often
both.

| Area | Library (`devicemanagement/`) | Reference server (`server/`) | Who owns the rest |
|---|---|---|---|
| Protocol and schema | Generated MDM, DDM, profile and status types with validation and platform metadata; plist and CMS codecs | Command validation against target metadata before queueing | The product developer decides which commands, profiles and declarations to send |
| Enrollment | Profile-based, Automated Device Enrollment, account-driven Device and User Enrollment, user channel and Shared iPad handling | Enrollment routes, admission policy loading, OIDC web authentication | The operator writes admission policy, runs the identity provider and sets up Apple Business Manager |
| Certificates | CA abstraction, SCEP, ACME, Managed Device Attestation, revocation | Issuer configuration, revocation services, rate limits | The operator supplies trust roots and HTTPS certificates and validates on real hardware |
| Apple services | APNs, device enrollment service, software lookup, Business and School Manager, Apps and Books clients | Push delivery, DEP sync worker, sealed credential storage | The operator obtains and renews Apple credentials |
| Declarative management | Engine, sets, membership, snapshots, status reports, predicates, Blueprint compiler and atomic publication | Blueprint API/CLI, configuration profile storage and delivery, synchronization into commands and pushes through the in-process adapter | The product developer authors declarations and builds fleet workflows; see [Blueprints](docs/operations/blueprints.md) |
| Storage | Contracts, in-memory implementations, contract suites | SQLite, PostgreSQL and MySQL stores with sealed secret columns | The operator runs the database, backups and key custody |
| Administration | Typed events and hooks | Admin API, managed principals and roles, Cedar policies, audit trail, native webhooks, `dmctl` | The operator manages accounts; the product developer builds integrations and dashboards |
| Product | None | None | The product developer builds the management UI, inventory, fleet policy and workflows |

Not planned:

- A management UI, inventory product or fleet policy engine.
- A hosted service.
- Compatibility with the internal service contracts of other implementations, such as
  NanoMDM's `-dm` header hop.
- Platforms other than Apple's.

## Design objectives

1. Library first. The server is one composition of the library, and the library never
   imports it ([0001](docs/research/decisions/0001-architecture.md),
   [0044](docs/research/decisions/0044-repository-layout.md)).
2. Generated from Apple's pinned schema. Types, validation and support metadata are
   regenerated, never hand-edited, and a pinned historical schema keeps older devices
   manageable ([0003](docs/research/decisions/0003-schema-generator.md),
   [0046](docs/research/decisions/0046-generated-from-is-generated.md),
   [0052](docs/research/decisions/0052-mixed-os-fleets.md)).
3. MDM and DDM together. Declarative management extends the MDM enrollment instead of
   running beside it ([0039](docs/research/decisions/0039-ddm-is-an-extension-of-mdm.md)).
4. Schema-defined Apple platforms. Availability is evaluated against each device's
   observed OS, version, channel and capabilities. The pinned compatibility schema
   retains support for older devices; individual features keep Apple's release floors.
5. Storage-agnostic. Domain contracts and a shared contract suite define backend behavior for
   memory, SQLite, PostgreSQL and MySQL
   ([0005](docs/research/decisions/0005-storage-interfaces.md),
   [0012](docs/research/decisions/0012-sql-storage-backends.md)).
6. Secure defaults. Admission is denied unless policy permits it, revocation is on, secret
   columns are sealed and event sinks redact by default
   ([0037](docs/research/decisions/0037-event-sinks-and-redaction.md),
   [0047](docs/research/decisions/0047-enrollment-authentication-and-optional-security-services.md),
   [0050](docs/research/decisions/0050-enrollment-security-boundaries.md)).
7. Verifiable. Simulator scenarios, contract suites, a 95% coverage floor, recorded decisions
   and a physical-device bench back each capability
   ([0048](docs/research/decisions/0048-reference-server-bench.md)).
8. Explicit boundaries. Trust roots, Apple credentials, enrollment policy and the user
   interface belong to the consumer.

The root module `github.com/deploymenttheory/go-apple-dm` holds the library under
[devicemanagement/](devicemanagement/): protocol packages, generated schema types, PKI, Apple
clients, storage contracts and in-memory implementations. The `server` module adds SQL stores,
the service layer, HTTP adapters, administration and application wiring. Both modules require
Go 1.27.

> [!WARNING]
> This project is in beta. While it has been tested extensively, please thoroughly test in non-production environments before production use. Features may contain bugs or undergo changes based on community feedback. No guarantees or official support is provided. Use at your own risk. By using this project, you acknowledge and agree to these conditions. For questions or issues, please consult the documentation or contact the maintainer.

> [!TIP]
> This is a community-driven project and is not officially supported by Apple.

This project generates its MDM and DDM functionality by parsing the schema from Apple's [Device Management](https://github.com/apple/device-management) project.

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
DM_STORAGE=inmem DM_BOOTSTRAP_TOKEN=dev-token go run ./server/cmd/dmserver
```

In another terminal:

```sh
curl http://127.0.0.1:8080/healthz
umask 077
go run ./server/cmd/dmctl -server http://127.0.0.1:8080 -token dev-token \
  -output human auth bootstrap local-root > /tmp/dm-local-root-token
go run ./server/cmd/dmctl -server http://127.0.0.1:8080 -token @/tmp/dm-local-root-token status
```

This configuration loses state on restart. Device enrollment additionally requires a public
HTTPS endpoint, a matching push topic and certificate, and a configured enrollment identity
issuer. Read [enrollment security operations](docs/operations/enrollment-security.md) before
enabling enrollment. Behind a TLS proxy, restrict certificate-header trust to that proxy.
The listener defaults to `127.0.0.1:8080`. Remote listeners require
`DM_TLS_CERT_FILE` and `DM_TLS_KEY_FILE`; this also applies to container networks.
Remote `dmctl` connections require HTTPS with verified trust. Use `-ca-file`
or `DMCTL_CA_FILE` for a private CA. The former `-insecure` flag is rejected.

`DM_BOOTSTRAP_TOKEN` can only create the first stored root credential. The server
consumes it atomically; it never grants ordinary API access. Root administers
principals, managed roles and policies. Device operations require explicit Cedar
permits, including for root. See [access control](docs/operations/access-control.md)
for roles, safe action groups, bootstrap, recovery and upgrade instructions.

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
- [Apps and Books licensing](docs/operations/apps-and-books.md) for device/user apps and
  user books, including user lifecycle, asynchronous outcomes and authenticated notifications.
- [Protocol helpers](docs/operations/protocol-helpers.md) for Managed Apple Account JWTs,
  ADE password hashes, FileVault CMS decryption, Activation Lock codes and SHA-256 manifests.
  FileVault encryption certificates are generated automatically in Go and stored encrypted.
- [Offline profile lint and paginated DDM inspection](docs/operations/status-and-profile-inspection.md)
  through `dmctl`, using generated schema metadata and existing status storage.
- In-memory, SQLite, PostgreSQL and MySQL persistence; column sealing and key rotation for
  selected secrets; optional shared security state and inbound rate limits.
- A device simulator, an admin API with scoped credentials and Cedar policies, and projected
  event sinks, SQL event delivery and audit records.
- An opt-in [content-cache metrics library](devicemanagement/contentcache/) based on
  Apple's OS 27 seed OpenAPI, with an embeddable receiver and caller-owned authentication
  and storage. Consumers mount the receiver and provide authorization and acceptance callbacks.

Simulator tests verify the modeled protocol exchanges. Physical-device interoperability,
hardware attestation and deployment-specific trust configuration require separate validation.

## Reference server

`server/cmd/dmserver` runs MDM and DDM together as one device-management service.
DDM is an extension of the MDM enrollment. Runtime `mdm`, `ddm` and `all` modes
and the private forwarding listener have been removed. Reusable protocol adapters
remain available to custom applications.

Configuration is read from `DM_*` environment variables. This table groups the main settings;
[server/internal/app/env.go](server/internal/app/env.go) defines their parsing and defaults.

| Variables | Purpose |
|---|---|
| `DM_LISTEN`, `DM_STORAGE`, `DM_DSN` | Listen address (default `127.0.0.1:8080`), backend (`sqlite`, `postgres`, `mysql`, `inmem`), and DSN |
| `DM_TLS_CERT_FILE`, `DM_TLS_KEY_FILE` | Server TLS certificate and private key; required for a non-loopback listener |
| `DM_BOOTSTRAP_TOKEN` | One-time secret accepted only by `POST /admin/v1/auth/bootstrap`; creates the first root without fleet permissions. The principal/role/policy store is always enabled |
| `DM_STORAGE_KEYS`, `DM_STORAGE_KEY_<NAME>`, `DM_SECRETS_DIR`, `DM_STORAGE_KEYS_STRICT` | Keys sealing the secret columns of a persistent store: escrow and private keys, raw check-ins and command/results, protocol state and credential-bearing declarations. `DM_STORAGE_KEYS` lists key names active-first, and the material comes from `DM_STORAGE_KEY_<NAME>` or from files in `DM_SECRETS_DIR`. A rotation prepends a name and runs `Rewrap`; `DM_STORAGE_KEYS_STRICT` then refuses any row still in clear. A persistent backend will not start without this |
| `DM_ALLOW_REENROLL` | Permit a changed certificate during `Authenticate`. Disabled by default in both the reusable service and reference server. Enable only with an enrollment authorization policy that permits the replacement; certificate chain validation alone does not bind a certificate to an enrollment identifier |
| `DM_DDM_SUBSCRIPTIONS` | Synthesize automatic status subscription declarations |
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
| `DM_AUDIT_STORE`, `DM_AUDIT_RETENTION` | Enable the separate projected audit trail on this process's database and set its retention (unset keeps records forever). SQL event capture is enabled independently with SQL storage. Read it at `GET /admin/v1/audit` or with `dmctl audit list --since 1h` |
| `DM_AUDIT_LOG` | Projected slog event records; off by default |
| `DM_WEBHOOKS_ENABLED`, `DM_WEBHOOK_ROOT_CA_FILE`, `DM_WEBHOOK_PRIVATE_NETWORKS` | Managed native webhooks with verified HTTPS, mandatory signatures and explicitly allowed private receiver CIDRs. SQL, storage keys and admin authentication are required. See [webhook setup and JSON examples](docs/operations/webhooks.md) |
| `DM_WEBHOOK_PAYLOAD_RETENTION`, `DM_WEBHOOK_METADATA_RETENTION`, `DM_WEBHOOK_MAX_BODY_BYTES` | Defaults: `168h`, `720h`, `16777216`. Retain encrypted bodies separately from delivery metadata and bound each observed body |
| `DM_DEP_BASE_URL`, `DM_DEP_SYNC_INTERVAL`, `DM_DEP_ASSIGN_INTERVAL`, `DM_DEP_PROFILE_URL`, `DM_DEP_USE_PUT` | Device enrollment service endpoint, independent sync and assignment intervals (zero disables that worker), and the DEP profile URL (defaults to this server) |
| `DM_PUSH_SOURCE`, `DM_PUSH_CERT_FILE`, `DM_PUSH_KEY_FILE`, `DM_PUSH_HOST`, `DM_PUSH_COALESCE`, `DM_PUSH_CERT_TTL` | Where APNs credentials come from and how pushes are shaped: `off`, `file` (the PEM pair, selected implicitly when a certificate file is configured) or `store` (the push certificate store). The APNs topic is derived from the push certificate; `DM_PUSH_TOPIC` separately configures the enrollment profile topic; `DM_PUSH_HOST` overrides the APNs endpoint for a lab, `DM_PUSH_COALESCE` is the window repeated pushes collapse into (negative disables it), and `DM_PUSH_CERT_TTL` how long a store-backed certificate is cached before its version is rechecked |
| `DM_PKI_REVOCATION`, `DM_RATE_LIMITS` | Certificate revocation is enabled by default; inbound rate limiting requires configuration. See [enrollment security operations](docs/operations/enrollment-security.md) for their configuration and operational requirements. |

[Certificate setup and renewal](docs/operations/certificate-lifecycle.md) documents `dmctl setup`,
encrypted persistent state, Apple portal handoffs, HTTPS renewal, and enrollment CA migration.
Use `dmserver --setup-file path/to/setup.json` to load managed identities.

SQL backends retain projected events and audit/webhook delivery state across restart;
`dmctl events status` and `dmctl events list --state blocked` expose failures. Slog and
in-memory bus delivery remain ephemeral. See [event delivery and retry](docs/operations/event-delivery.md)
for guarantees, pagination, destination changes and retention boundaries.

`server/cmd/dmctl` provides typed commands for enrollments, queued commands, push certificates,
declarations, sets, notifications, principals, policies, audit records and event delivery. `dmctl routes`
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
and the pinned [device-management schema](third_party/apple-device-management/current/) define the protocol.
The [reference catalogue](docs/research/reference_projects.md) records additional sources.
[Design decisions](docs/research/decisions/README.md) explain this project's implementation choices.

## License

MIT. See [LICENSE](LICENSE).
