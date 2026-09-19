// Package dmctl implements command dispatch, configuration and output for the
// administrative CLI.
//
// # Design
//
// server/cmd/dmctl delegates to Run so behavior is testable without a
// subprocess. Configuration stores credential references by default; explicit
// inline storage requires an option and warning. Single-page JSON preserves
// server bytes; paginated -all JSON and NDJSON stream one item per line. Human
// output uses tables. HTTP handling and offline schema explanation live in
// subpackages. Exit codes distinguish usage, authorization, request failure and
// partial success.
//
// Utility commands use the library's identity discovery and App Settings
// constructors without initializing a management-server client. The CLI owns
// flags, bounded JSON input, tables and CSV exports. App Settings commands write
// validated payload JSON for the supplied target and matching policy.
//
// # References
//
//   - Utility guide: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/app-identity-and-settings.md
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0035-dmctl-structure-and-credentials.md
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0036-dmctl-explain-over-schema-support.md
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0034-admin-api-and-authorization.md
//   - E2E scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-024)
package dmctl
