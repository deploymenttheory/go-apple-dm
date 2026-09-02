# go-apple-mdm

A pure Go library for the Apple Mobile Device Management (MDM) protocol and Declarative Device
Management (DDM), with a thin reference server.

Status: pre-release, following the [implementation plan](docs/research/implementation_plan.md).
No API stability promise until v1.0.0.

## What it will provide

- MDM check-in and command channel with typed messages generated from Apple's schema
- Declarative Device Management engine with content-addressed declaration tokens
- APNs push, SCEP and ACME enrollment identities, DEP and Apple Business Manager clients
- Storage backends: in-memory, SQLite, PostgreSQL, MySQL, behind one contract-tested interface set
- A device simulator for testing MDM and DDM servers without hardware

## Layout

| Path | Purpose |
|---|---|
| `schema/` | Generated types from `third_party/device-management` (never hand-edited) |
| `internal/schemagen`, `cmd/admgen` | The generator |
| `mdm/` | Protocol core: enrollment identity, check-in decoding, command and response envelopes |
| `cms/` | Detached CMS signing and `Mdm-Signature` verification with signing-time tolerance |
| `service/` | Enrollment lifecycle, identity pinning, command delivery, hooks, events |
| `storage/` | Storage interfaces, in-memory backend, and the contract suite every backend runs |
| `storage/sqlcommon`, `storage/sqlite`, `storage/postgres`, `storage/mysql` | One SQL implementation with embedded migrations; SQLite (pure Go), PostgreSQL (pgx), and MySQL drivers; unlock tokens, bootstrap tokens, push keys, and user auth tokens are sealed columns when a keyring is configured |
| `storage/crypt` | AES-256-GCM sealing of secret columns under named keys from `secrets.Provider`, with row-bound AAD and in-place key rotation (`Rewrap`) |
| `httpapi/` | Check-in and server URL handlers plus certificate extraction middlewares |
| `push/`, `push/apns`, `push/pushcert` | Pusher interface, notifier with invalid-token events, coalescing, HTTP/2 APNs client, fake APNs server; push certificate parsing and topic derivation; a store-backed certificate cache that picks up renewals |
| `ca/`, `scep/` | Certificate authority abstraction and a SCEP endpoint with one-time and HMAC challenges, plus a client |
| `profile/`, `enroll/` | Configuration profile composition, signing, and parsing; MDM enrollment profile builder; OTA profile service |
| `secrets/` | Redacting secret type and providers (static, environment, directory, chain) |
| `ddm/` | Declarative Device Management engine: declarations with content-addressed `ServerToken`s (RFC 8785 canonical JSON), sets and dynamic membership, per-enrollment snapshots so a device always fetches what its manifest advertised, status reports stored per item, synthesised status subscriptions, cleanup on CheckOut and re-enrollment, and a coalescing change notifier |
| `ddm/predicate`, `internal/canonjson` | The documented NSPredicate subset activations use (validated at upload, evaluated by the simulator); RFC 8785 canonicalisation over `encoding/json/jsontext` |
| `ddm/inmem`, `ddm/sqlstore`, `ddm/ddmtest` | Engine stores (in-memory; SQLite, PostgreSQL, MySQL on their own migration set) and the contract suite both run |
| `ddm/adapter/inproc`, `ddm/adapter/proxyclient`, `ddm/adapter/proxyserver` | DDM in-process, or split across our own `mdm` and `ddm` roles: Apple's DeclarativeManagement check-in forwarded verbatim over an HMAC-signed or mTLS hop |
| `internal/app`, `cmd/mdmserver`, `Dockerfile` | Reference server roles (`mdm`, `ddm`, `all`), `/healthz`, a minimal admin API, and the container CI builds from this repository for the split-deployment scenario |
| `simulator/` | Device simulator for testing servers without hardware, including a DDM client that syncs, evaluates predicates, and reports status the way Apple's reason codes describe |
| `e2e/` | End-to-end scenarios (`make test-e2e`) |
| `docs/research/` | Reference research, the plan of record, and per-feature decision records |
| `docs/security/threat-model.md` | STRIDE threat model, updated every phase |
| `docs/testing/e2e-scenarios.md` | Named end-to-end scenarios mapped to Apple documentation |

## Development

```bash
git submodule update --init   # pinned Apple schema
make ci                       # lint, verify, test, storage, e2e, fuzz smoke, coverage gate
make testdb-up                # PostgreSQL and MySQL in Docker for `make test-storage` and `E2E_STORE=postgres make test-e2e`
make test-storage-perf        # the 100k-row Clear timing gate on PostgreSQL, without the race detector
make testdb-down              # remove the Docker test databases
make testdb-ddm-up            # build our image and run the ddm role for `TestE2E_DDMSplitDeployment`
make testdb-ddm-down          # remove the ddm role container
```

Coverage floor is 95% overall and per package. See `Makefile` targets with `make help`.

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
