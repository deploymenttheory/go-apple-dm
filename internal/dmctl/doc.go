// Package dmctl implements the admin CLI. cmd/dmctl is a main that parses
// argv and calls Run; everything else lives here.
//
// # Why
//
// The split is forced by arithmetic rather than taste. scripts/coverage-gate.sh
// computes the repository figure from every package in the merged profile,
// including exempt ones, and make test runs with -coverpkg=./..., so an
// uncovered command package still drags the total down. The cmd/ exemption
// suppresses the per-package line and nothing more. Keeping the logic here
// means it is gated at 95% like everything else; micromdm's cmd/dmctl is
// 3185 lines with three trivial tests, and nanohubctl has none.
//
// Two behaviours are worth knowing about. Credentials are referenced, never
// stored: the config file holds the name of an environment variable or a file
// path, and there is no subcommand whose job is printing a secret. And the
// output modes are separate on purpose, with -output json emitting the
// server's bytes unchanged so canonical JSON survives to jq.
//
// # References
//
//   - Decision record: docs/research/decisions/0035-mdmctl-structure-and-credentials.md
//   - Decision record: docs/research/decisions/0036-mdmctl-explain-over-schema-support.md
//   - Decision record: docs/research/decisions/0034-admin-api-and-authorization.md
//   - Plan of record: docs/research/implementation_plan.md (phase 8)
//   - E2E scenarios: docs/testing/e2e-scenarios.md (E2E-024)
package dmctl
