// Command mdmctl administers a go-apple-dm reference server: declarations,
// admin credentials, the policies that bound them, and an offline explain
// over Apple's schema metadata.
//
// # Why
//
// The admin API needs a first-party client, and an operator needs one command
// that can answer "what does this credential allow" and "does this key apply
// to a supervised Mac on 15.0" without curl and without a browser tab.
//
// This file and main.go are the whole binary: argv in, one call out. Every
// other line lives in internal/mdmctl, which is gated at 95% like the rest of
// the module. That is forced rather than stylistic: the coverage gate counts
// statements from exempt packages toward the repository total, so a fat main
// would fail the gate even though cmd/ is exempt per package.
//
// # Usage
//
//	mdmctl [flags] <command> [flags] [arguments]
//
//	explain <id> [-target macos:15.0,supervised]  describe a schema type, offline
//	status                                        the server's role, families, version
//	routes                                        the admin routes this server serves
//	actions                                       what each grantable action means
//	principals list|get|create|rotate|revoke|delete|set-roles
//	policies   list|get|put|delete
//	declarations get|put|delete
//	version
//
//	-server URL      the server (MDMCTL_SERVER)
//	-token SPEC      a token, @file, or env:NAME (MDMCTL_TOKEN)
//	-output MODE     human, json, or ndjson
//	-all             follow cursors to the end of a listing
//
// explain needs no server. Global flags are accepted before or after the
// command, so both orderings work.
//
// # References
//
//   - Decision record: docs/research/decisions/0035-mdmctl-structure-and-credentials.md
//   - Decision record: docs/research/decisions/0036-mdmctl-explain-over-schema-support.md
//   - Decision record: docs/research/decisions/0034-admin-api-and-authorization.md
//   - Plan of record: docs/research/implementation_plan.md (phase 8)
//   - E2E scenarios: docs/testing/e2e-scenarios.md (E2E-024)
package main
