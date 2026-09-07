# go-apple-dm

Go libraries for Apple's MDM protocol and declarative device management, with a reference server.
Read [CONTRIBUTING.md](CONTRIBUTING.md) for editorial and validation requirements and
[docs/architecture.md](docs/architecture.md) for the implemented design.

## Modules and dependencies

The root module is `github.com/deploymenttheory/go-apple-dm`; `server/` is
`github.com/deploymenttheory/go-apple-dm/server`. Both use Go 1.27. The server depends on the
library. Library code and tests must not import the server. `go.work` supports local development.
`internal/layout` enforces the tier constraints and explicit exceptions.

Do not add a dependency on `deploymenttheory/go-sdk-appleservices`. NanoMDM, MicroMDM and
related repositories under `third_party/refs` are read-only references, not code dependencies;
`github.com/micromdm/plist` is the accepted exception. Consult relevant primary sources and
implementation references when evaluating protocol behavior. Do not copy third-party code.

## Generated files

`make generate` uses the pinned `third_party/device-management` submodule. Never hand-edit
`*.gen.go`, `schema/EXPORTED_IDENTIFIERS.lock` or `schema/GENERATED_FROM.json`; `make verify`
checks deterministic regeneration and exported-name removals. Edit generator documentation
at its source. Preserve Apple's verbatim descriptions and exact protocol identifiers.

## Design and comments

Use the current-design format in [docs/research/decisions/TEMPLATE.md](docs/research/decisions/TEMPLATE.md)
for significant decisions. Integrate amendments without changing decision filenames or numbers.
Explain behavior, contracts, rationale and limitations; omit implementation chronology and
comparative claims. Keep package comments in `doc.go` with a concise summary, useful sections
and references. Generated packages follow the corresponding layout in `doc.gen.go`.

## Checks

- `make verify`: generation and exported-name guard.
- `make test`: both modules with race detection and coverage.
- `make testdb-up`, then `make test-storage`: SQL contract tests using the printed DSNs.
- `make test-e2e`: simulator scenarios; `make fuzz-smoke`: bounded fuzz runs.
- `make coverage`: 95% overall and per non-exempt package; see `scripts/coverage-exempt.txt`.
- Lint each module with `--fix=false` when automatic rewriting is inappropriate.

Add failure-path coverage for exported functions. Use Conventional Commits; release-please owns
release versions and changelogs. See `make help` for complete command requirements.
