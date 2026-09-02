# go-apple-mdm

Pure Go library for the Apple MDM protocol and Declarative Device Management, plus a thin
reference server. Module `github.com/deploymenttheory/go-apple-mdm`, Go 1.27.

## Plan of record

- `docs/research/implementation_plan.md`: approved phased plan. Follow its phase order and exit criteria.
- `docs/research/reference_projects.md`: the research every decision is founded on.
- `docs/research/decisions/`: one decision record per feature, from `TEMPLATE.md`. No feature code
  lands without one.

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
- Every package has a `doc.go` holding its only package comment, laid out like `ddm/doc.go`: a
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
