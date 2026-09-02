// Package main is the mdmserver command: it runs the reference server in one of three roles: mdm
// (check-in and connect), ddm (the declarative management engine behind
// the internal hop and the admin API), or all (both in one process).
//
// # Why
//
// CI and the split-deployment scenario (E2E-010) need our own binary in a
// container, built from this repository, so both ends of the internal
// wire contract are ours. The command is a thin shell over internal/app:
// it reads MDM_* variables, lets flags override them, serves HTTP, and
// offers -check for container health probes without a shell. Everything
// else lives in the library packages so it stays testable.
//
// # Usage
//
//	mdmserver [-role mdm|ddm|all] [-listen :8080] [-storage sqlite|postgres|mysql|inmem]
//	          [-dsn PATH_OR_DSN] [-ddm-url URL] [-ddm-send-key K] [-ddm-recv-key K]
//	          [-admin-token T] [-ca-file PEM | -cert-header NAME] [-ddm-subscriptions=true]
//	mdmserver -check http://127.0.0.1:8080/healthz
//
// # References
//
//   - Decision record 0025: docs/research/decisions/0025-reference-server-roles-and-container.md
//   - Plan of record: docs/research/implementation_plan.md (phase 5, phase 8)
//   - Container: Dockerfile, scripts/testdb.sh (ddm-up), .github/workflows/go-test.yml (e2e job)
package main
