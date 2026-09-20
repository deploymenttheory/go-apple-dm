// Package main runs the unified device-management reference server.
//
// # Design
//
// The command reads DM_* variables, applies flag overrides and delegates
// composition to server/internal/app. One runtime serves device ingress and
// the declaration engine through the in-process adapter.
// Administrative families depend on configured components and credentials. TLS
// termination belongs to the deployment.
//
// # Usage
//
//	dmserver -storage inmem -bootstrap-token dev-token
//	dmserver -check auto
//
// Use -help for current flags. The -check mode supports container health probes
// without a shell. Persistent storage, enrollment, authentication and optional
// security services require the configuration documented in the README and
// operations guide.
//
// # References
//
//   - Decision record 0056: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0056-unified-server-rbac.md
//   - Container: Dockerfile, scripts/testdb.sh, .github/workflows/go-test.yml (e2e job)
package main
