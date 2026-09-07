//go:build e2e

// Package e2e runs named protocol scenarios against the assembled reference
// server and device simulator.
//
// # Design
//
// The e2e build tag enables these tests. E2E_STORE selects SQLite, PostgreSQL or
// in-memory MDM storage; SQLite is the default. Scenarios exercise signed
// requests, enrollment, commands, push fakes, declarative management,
// administration and optional security controls. The split-deployment scenario
// can use a ddm container built from this repository.
//
// Run make test-e2e. External database and container cases require their
// documented environment settings and can skip when absent. Simulator-based
// results do not establish physical Apple-device interoperability.
//
// # References
//
//   - E2E scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (each control names its e2e proof)
//   - Decision record 0025: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0025-reference-server-roles-and-container.md
//   - Container and databases: scripts/testdb.sh (up, ddm-up)
package e2e
