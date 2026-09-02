//go:build e2e

// Package e2e runs the named end-to-end scenarios against a real HTTP
// server built from the library, with the device simulator as the client.
//
// # Why
//
// Unit and contract suites prove each package on its own; the scenarios in
// docs/testing/e2e-scenarios.md prove the protocol paths Apple documents
// end to end: service core, a storage backend chosen by E2E_STORE (sqlite by
// default; postgres or inmem), HTTP handlers with Mdm-Signature
// verification, SCEP and OTA enrollment, push through a fake APNs, and the
// declarative management engine in-process or, for E2E-010, split across
// our own mdm and ddm roles with the ddm role in a container built from
// this repository. The package is a test-only package behind the e2e build
// tag and is run by "make test-e2e".
//
// # References
//
//   - E2E scenarios: docs/testing/e2e-scenarios.md
//   - Plan of record: docs/research/implementation_plan.md (exit criteria per phase)
//   - Threat model: docs/security/threat-model.md (each control names its e2e proof)
//   - Decision record 0025: docs/research/decisions/0025-reference-server-roles-and-container.md
//   - Container and databases: scripts/testdb.sh (up, ddm-up)
package e2e
