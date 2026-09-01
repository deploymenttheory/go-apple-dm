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
| `httpapi/` | Check-in and server URL handlers plus certificate extraction middlewares |
| `simulator/` | Device simulator for testing servers without hardware |
| `e2e/` | End-to-end scenarios (`make test-e2e`) |
| `docs/research/` | Reference research, the plan of record, and per-feature decision records |
| `docs/security/threat-model.md` | STRIDE threat model, updated every phase |
| `docs/testing/e2e-scenarios.md` | Named end-to-end scenarios mapped to Apple documentation |

## Development

```bash
git submodule update --init   # pinned Apple schema
make ci                       # lint, verify, test, storage, e2e, fuzz smoke, coverage gate
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
