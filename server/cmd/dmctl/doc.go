// Command dmctl administers the reference server and explains compiled Apple
// schema metadata offline.
//
// # Design
//
// The entry point delegates argument handling and execution to
// server/internal/dmctl so CLI behavior can be tested directly. Global flags
// work before or after a command. Use dmctl help and each command's -help for
// the current verb and option list. JSON output preserves server bytes, while
// human and NDJSON modes support interactive and streaming use.
//
// # Usage
//
//	dmctl [flags] <command> [flags] [arguments]
//	dmctl explain DeviceInformation -target macos:15.0,supervised
//	dmctl -server http://localhost:8080 -token env:DM_ADMIN_TOKEN status
//	dmctl api GET /admin/v1/routes
//
// Configuration can select server, token references, context and output through
// DMCTL_* variables or a restricted config file. The default config path is
// $XDG_CONFIG_HOME/go-apple-dm/dmctl.json, with a home-directory fallback.
// explain requires neither a server nor credentials.
//
// # References
//
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0035-dmctl-structure-and-credentials.md
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0036-dmctl-explain-over-schema-support.md
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0034-admin-api-and-authorization.md
//   - E2E scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-024)
package main
