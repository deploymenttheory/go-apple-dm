// Package dmctl implements command dispatch, configuration and output for the
// administrative CLI.
//
// # Design
//
// server/cmd/dmctl delegates to Run so behavior is testable without a
// subprocess. Configuration stores credential references by default; explicit
// inline storage requires an option and warning. JSON output preserves server
// bytes, while human and NDJSON modes provide table and streaming output. HTTP
// handling and offline schema explanation live in subpackages. Exit codes
// distinguish usage, authorization, request failure and partial success.
//
// # References
//
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0035-dmctl-structure-and-credentials.md
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0036-dmctl-explain-over-schema-support.md
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0034-admin-api-and-authorization.md
//   - E2E scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-024)
package dmctl
