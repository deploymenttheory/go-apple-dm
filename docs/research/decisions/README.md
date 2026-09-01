# Decision records

One record per feature, written before the feature's code, using [TEMPLATE.md](TEMPLATE.md).
This is step 3 of the research-guided build loop in
[../implementation_plan.md](../implementation_plan.md) section 8.

## Per-feature checklist

Before code:

- [ ] Apple documentation page and the YAML file(s) under `third_party/device-management` located
- [ ] At least two reference implementations read (`make refs` clones them under `third_party/refs/`)
- [ ] Reference issue trackers and commit history mined for pitfalls (commands in the plan, section 8)
- [ ] Decision record written; every "what we do better" claim names a test

With the code:

- [ ] Package doc comment cites the Apple page and YAML file(s)
- [ ] Tests for every pitfall found, written first
- [ ] Every new exported function has at least one failing-path test
- [ ] Fuzz target added if the change parses untrusted input
- [ ] `make generate` and `make verify` clean if schema packages changed
- [ ] `make ci` green, coverage gate at or above 95%
- [ ] Threat model updated if an endpoint or trust boundary changed
- [ ] Record status moved to `accepted`

## Index

| ID | Title | Status | Phase |
|---|---|---|---|
| [0001](0001-architecture.md) | Library-first architecture with a generated schema core | accepted | 0 |
| [0002](0002-plist-library.md) | plist encoding and decoding | accepted | 0 |
| [0003](0003-schema-generator.md) | In-repo schema generator over apple/device-management | accepted | 1 |
