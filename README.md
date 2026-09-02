# go-apple-mdm

A pure Go library for Apple device management: the MDM check-in and command protocol,
Declarative Device Management (DDM), every enrollment path Apple documents (profile, automated,
account-driven, user channel and Shared iPad), and clients for the device enrollment service and
the Apple Business Manager API. A thin reference server, `cmd/mdmserver`, wires it all together.

Status: pre-release, phases 1 to 6 of the [implementation plan](docs/research/implementation_plan.md)
are delivered. No API stability promise until v1.0.0.

## What it provides

- **Protocol core.** Typed check-in, command, and response messages generated from Apple's
  `device-management` schema, never hand-edited. Detached and attached CMS verification.
- **Service layer.** Enrollment lifecycle, identity pinning, command queue with dedupe keys,
  hooks and an event bus, user-channel and Shared iPad handling, optional user authentication
  gate, and schema-driven checks that a command is supported by the enrolled OS and version.
- **Storage.** In-memory, SQLite (pure Go), PostgreSQL, and MySQL behind one contract-tested
  interface set. Secret columns are sealed with AES-256-GCM under named keys with in-place
  rotation. Every schema lives in a single `0001_init.sql` per dialect per migration set.
- **Push.** APNs HTTP/2 client, notifier with invalid-token events, coalescing, push certificate
  parsing and topic derivation, and a fake APNs server for tests.
- **Enrollment identities.** A certificate authority abstraction and a SCEP service with
  one-time and HMAC challenges, plus a client.
- **Enrollment paths.** Profile enrollment with an OTA profile service; automated device
  enrollment with typed `MachineInfo` parsing, CMS signature verification against Apple's
  device CAs, the software update gate, and an OIDC web view; service discovery for
  account-driven enrollment with both the `apple-as-web` and `apple-oauth2` flows on an
  authorization server of our own; user channel enrollment and Shared iPad users.
- **Device enrollment service (DEP).** OAuth 1.0a session client with cursor handling, token
  PKI for the portal exchange, a device syncer, a profile assigner with read-back, in-memory and
  SQL stores, and a fake service for tests.
- **Apple Business Manager API.** ES256 client assertion, JSON:API paging, device and server
  listing, assignment activities with convergence, rate-limit handling, and a fake server.
- **Declarative Device Management.** Declarations with content-addressed `ServerToken`s
  (RFC 8785 canonical JSON), sets and dynamic membership, per-enrollment snapshots so a device
  always fetches what its manifest advertised, status reports stored per item, synthesised
  status subscriptions, an NSPredicate subset validated at upload, and a coalescing change
  notifier. The engine runs in-process or split across our own `mdm` and `ddm` roles.
- **Device simulator.** MDM, DDM, ADE, account-driven, user channel, and Shared iPad clients so
  a server can be tested without hardware.
- **Reference server.** Roles `mdm`, `ddm`, and `all`, a bearer-protected admin API, `MDM_*`
  environment configuration, `/healthz`, and a distroless container image built by CI.

## Layout

| Path | Purpose |
|---|---|
| `schema/` | Generated types from `third_party/device-management` (never hand-edited); `schema/support` answers whether a command or key applies to an OS and version |
| `internal/schemagen`, `cmd/admgen` | The generator |
| `mdm/` | Protocol core: enrollment identity, check-in decoding, command and response envelopes |
| `cms/` | Detached and attached CMS signing and verification, `Mdm-Signature` with signing-time tolerance |
| `service/` | Enrollment lifecycle, identity pinning, command delivery, hooks, events, user channels, target validation |
| `storage/` | Storage interfaces, in-memory backend, and the contract suite every backend runs |
| `storage/sqlcommon`, `storage/sqlite`, `storage/postgres`, `storage/mysql` | One SQL implementation with embedded migrations for SQLite (pure Go), PostgreSQL (pgx), and MySQL; secret columns sealed when a keyring is configured |
| `storage/crypt` | AES-256-GCM sealing of secret columns under named keys from `secrets.Provider`, with row-bound AAD and in-place key rotation |
| `httpapi/` | Check-in and server URL handlers plus certificate extraction middlewares |
| `push/`, `push/apns`, `push/pushcert` | Pusher interface, notifier, coalescing, HTTP/2 APNs client and fake server; push certificate parsing, topic derivation, and a store-backed certificate cache |
| `ca/`, `scep/` | Certificate authority abstraction and a SCEP endpoint with one-time and HMAC challenges, plus a client |
| `profile/`, `enroll/` | Configuration profile composition, signing, and parsing; MDM enrollment profile builder (device, user, Shared iPad); OTA profile service |
| `enroll/ade`, `enroll/adetest` | Automated device enrollment: `MachineInfo` parsing and CMS verification, the software update gate, web view resume and finish, DEP lookup and policy hooks; fixtures and a fake device CA |
| `enroll/webauth`, `enroll/webauthtest` | OpenID Connect relying party for the ADE web view and account-driven pages; a fake identity provider |
| `enroll/discovery` | The `/.well-known/com.apple.remotemanagement` service discovery document, per user type |
| `enroll/accountdriven` | Account-driven enrollment: the `Bearer` challenge, `apple-as-web` and `apple-oauth2` flows, token issuance, and the check-in hook that ties the enrollment to the authenticated account |
| `dep/`, `dep/inmem`, `dep/sqlstore`, `dep/deptest` | Device enrollment service client (OAuth 1.0a, sessions, cursors, token PKI), device syncer, profile assigner, stores, contract suite, and the fake service |
| `axm/`, `axm/axmtest` | Apple Business Manager API client (ES256 client assertion, JSON:API paging, activities) and its fake server |
| `gdmf/`, `gdmf/gdmftest` | Apple's software update catalogue client for the ADE software update gate, with a fake |
| `secrets/` | Redacting secret type and providers (static, environment, directory, chain) |
| `ddm/` | Declarative Device Management engine: content-addressed declarations, sets and membership, snapshots, status reports, status subscriptions, cleanup on CheckOut, change notifier |
| `ddm/predicate`, `internal/canonjson` | The NSPredicate subset activations use; RFC 8785 canonicalisation over `encoding/json/jsontext` |
| `ddm/inmem`, `ddm/sqlstore`, `ddm/ddmtest` | Engine stores on their own migration set and the contract suite both run |
| `ddm/adapter/inproc`, `ddm/adapter/proxyclient`, `ddm/adapter/proxyserver` | DDM in-process, or split across our own `mdm` and `ddm` roles over an HMAC-signed or mTLS hop |
| `internal/app`, `cmd/mdmserver`, `Dockerfile` | The reference server: roles, enrollment routes, admin API, background workers, and the container image |
| `simulator/` | Device simulator: MDM, DDM, ADE, account-driven, user channel, and Shared iPad clients |
| `e2e/` | End-to-end scenarios (`make test-e2e`), listed in `docs/testing/e2e-scenarios.md` |
| `docs/research/` | Reference research, the plan of record, and per-feature decision records |
| `docs/security/threat-model.md` | STRIDE threat model, updated every phase |

## Reference server

`cmd/mdmserver` runs one of three roles. `mdm` serves devices (`/mdm`, `/scep`, the
enrollment routes) and forwards DDM check-ins to a `ddm` role when `MDM_DDM_URL` is set; `ddm`
runs the engine and the admin API; `all` runs everything in one process. Configuration is by
environment:

| Variables | Purpose |
|---|---|
| `MDM_ROLE`, `MDM_LISTEN`, `MDM_STORAGE`, `MDM_DSN` | Role, listen address, backend (`sqlite`, `postgres`, `mysql`, `inmem`), and DSN |
| `MDM_ADMIN_TOKEN` | Bearer token for `/admin/v1/` |
| `MDM_DDM_URL`, `MDM_DDM_SEND_KEY`, `MDM_DDM_RECV_KEY`, `MDM_DDM_SUBSCRIPTIONS` | The split-deployment hop and synthesised status subscriptions |
| `MDM_CA_FILE`, `MDM_CERT_HEADER` | Client certificate verification, direct or behind a proxy |
| `MDM_PUBLIC_URL`, `MDM_PUSH_TOPIC` | Turn on the enrollment routes; the server URL devices are given and the push topic |
| `MDM_ENROLL_CA_CERT_FILE`, `MDM_ENROLL_CA_KEY_FILE`, `MDM_SCEP_CHALLENGE`, `MDM_SCEP_HMAC_KEY` | The enrollment identity CA and its SCEP challenge; a self-signed CA is generated for development |
| `MDM_PROFILE_IDENTIFIER`, `MDM_ORGANIZATION` | Enrollment profile identity |
| `MDM_DISCOVERY`, `MDM_ACCOUNT_DRIVEN_METHOD` | Service discovery per user type (`Mac=mdm-adde,iPhone=mdm-byod`) and the account-driven flow (`apple-as-web` or `apple-oauth2`) |
| `MDM_OIDC_ISSUER`, `MDM_OIDC_CLIENT_ID`, `MDM_OIDC_CLIENT_SECRET` | The identity provider behind the ADE web view and account-driven pages |
| `MDM_ADE_ANCHOR_FILE`, `MDM_ADE_AUDIT`, `MDM_REQUIRE_USER_AUTH` | Extra `MachineInfo` signing anchors, audit-only signature policy, and the user authentication gate |
| `MDM_AXM_CLIENT_ID`, `MDM_AXM_KEY_ID`, `MDM_AXM_KEY_FILE`, `MDM_AXM_SCOPE`, `MDM_AXM_BASE_URL`, `MDM_AXM_TOKEN_URL` | Apple Business Manager API credentials; enables `/admin/v1/axm/` |
| `MDM_DEP_BASE_URL`, `MDM_DEP_SYNC_INTERVAL`, `MDM_DEP_ASSIGN_INTERVAL`, `MDM_DEP_PROFILE_URL`, `MDM_DEP_USE_PUT` | Device enrollment service endpoint, the background sync worker, and the DEP profile url (defaults to this server) |

The admin API manages declarations, sets, and assignments (`/admin/v1/declarations`, `/sets`,
`/enrollments`), Business Manager servers, devices, and activities (`/admin/v1/axm/`), and DEP
accounts: token PKI generation, `.p7m` import, device listing, profile definition, and sync
(`/admin/v1/dep/accounts/`). The exact routes and constants are in `internal/app`.

## Development

```bash
git submodule update --init   # pinned Apple schema
make ci                       # lint, verify, test, storage, e2e, fuzz smoke, coverage gate
make testdb-up                # PostgreSQL and MySQL in Docker for `make test-storage` and `E2E_STORE=postgres make test-e2e`
make test-storage-perf        # the 100k-row Clear timing gate on PostgreSQL, without the race detector
make testdb-down              # remove the Docker test databases
make testdb-ddm-up            # build our image and run the ddm role for `TestE2E_DDMSplitDeployment`
make testdb-ddm-down          # remove the ddm role container
make refs                     # clone the reference projects for research (never imported)
```

Coverage floor is 95% overall and per package. See `Makefile` targets with `make help`.

Dependencies stay minimal on purpose: the plist codec, the smallstep CMS and SCEP libraries,
and the SQL drivers. OAuth 1.0a, the ES256 client assertion, the OIDC relying party, and the
OAuth 2 authorization server are implemented in this module. Nothing from NanoMDM or MicroMDM is imported beyond the plist
package.

## Sources

Apple's [Device Management documentation](https://developer.apple.com/documentation/devicemanagement)
and the [apple/device-management](https://github.com/apple/device-management) schema repository are
the primary sources. The open source projects this work learns from are catalogued in
[docs/research/reference_projects.md](docs/research/reference_projects.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and the decision record process in
[docs/research/decisions/README.md](docs/research/decisions/README.md).

## License

MIT. See [LICENSE](LICENSE).
