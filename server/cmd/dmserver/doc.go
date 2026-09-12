// Package main runs the reference server in mdm, ddm or all mode.
//
// # Design
//
// The command reads DM_* variables, applies flag overrides and delegates
// composition to server/internal/app. The mdm role serves device ingress; ddm
// serves a declaration engine through the internal adapter; all combines them.
// Administrative families depend on configured components and credentials. TLS
// termination belongs to the deployment.
//
// # Usage
//
//	dmserver -role all -storage inmem -admin-token dev-token
//	dmserver -check auto
//
// Use -help for current flags. The -check mode supports container health probes
// without a shell. Persistent storage, enrollment, authentication and optional
// security services require the configuration documented in the README and
// operations guide.
//
// # References
//
//   - Decision record 0025: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0025-reference-server-roles-and-container.md
//   - Container: Dockerfile, scripts/testdb.sh (ddm-up), .github/workflows/go-test.yml (e2e job)
package main
