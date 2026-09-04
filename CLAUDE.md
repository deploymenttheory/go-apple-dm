# go-apple-dm

Pure Go library for the Apple MDM protocol and Declarative Device Management, plus a thin
reference server. Module `github.com/deploymenttheory/go-apple-dm/v3`, Go 1.27.

## Plan of record

- `docs/research/implementation_plan.md`: approved phased plan. Follow its phase order and exit criteria.
- `docs/research/reference_projects.md`: the research every decision is founded on.
- `docs/research/decisions/`: one decision record per feature, from `TEMPLATE.md`. No feature code
  lands without one.

## Layout

Packages sit in tiers and may import their own tier and every tier below, never above:
foundation (`paging`, `clock`, `testpki`, `secrets`, `telemetry`) -> `schema/` ->
`mdmprotocol/` -> `pki/` -> `appleplatformservices/` -> `storage/` -> `simulator/` -> `server/` ->
app (`cmd/`, `internal/app`, `e2e/`). `internal/layout` asserts this in tests, because
`go-lint.yml` runs golangci-lint with `--issues-exit-code=0` and cannot fail a build.
See `docs/research/decisions/0044-repository-layout.md`.

## Rules

- Read at least two reference implementations before writing a feature (`make refs` clones them
  under `third_party/refs/`, read-only). Never copy code from them.
- Generated code lives only under `schema/` and is produced by `make generate` from the pinned
  submodule `third_party/device-management`. Never hand-edit `*.gen.go`.
- Coverage floor is 95% overall and per non-exempt package (`scripts/coverage-exempt.txt`).
  Every exported function has a failing-path test.
- Do not add a dependency on `deploymenttheory/go-sdk-appleservices`.
- Do not add code dependencies on NanoMDM, MicroMDM, or their libraries; `github.com/micromdm/plist` is the only accepted exception. Their repositories are read-only references.
- Conventional commits; release-please manages versions and `CHANGELOG.md`.
- Every package has a `doc.go` holding its only package comment, laid out like `mdmprotocol/ddm/doc.go`: a
  one-sentence "what", a `# Why` section (the need it meets, where it sits in the plan, what it
  deliberately leaves out), and a `# References` section listing the decision records, plan
  phase, internal docs, Apple documentation, schema files, and RFCs it rests on. Generated
  `schema/*` packages get the same layout from `doc.gen.go` through the generator.

## Commands

```bash
make ci            # lint, verify, test, test-storage, test-e2e, fuzz-smoke, coverage
make test          # unit tests with race detector and coverage
make testdb-up     # PostgreSQL and MySQL in Docker; prints the TEST_*_DSN exports for test-storage
make test-storage-perf  # 100k-row Clear timing gate on PostgreSQL, no race detector
make generate      # regenerate schema packages
make verify        # deterministic regeneration + rename guard
make coverage      # enforce the 95% gate on collected profiles
```
