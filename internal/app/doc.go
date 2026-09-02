// Package app wires the reference server: storage, the MDM core, the
// Declarative Device Management engine, the adapters between the roles,
// the change notifier, a health endpoint, and a minimal admin API.
//
// # Why
//
// The library packages are deliberately independent; something has to
// assemble them into a process that CI can build into a container and run
// on the far side of a real network hop. Phase 5 of the plan of record
// brings that assembly forward in minimal form so E2E-010 (split
// deployment) exercises our own binary on both sides of the wire; phase 8
// grows it into the documented reference server. Three roles exist: "mdm"
// serves check-in and connect and reaches DDM either in-process or through
// ddm/adapter/proxyclient, "ddm" serves the engine behind
// ddm/adapter/proxyserver plus the admin API, and "all" runs both in one
// process. Roles that split across processes share one storage DSN. Push
// delivery and TLS termination are left to the deployment (phase 8).
//
// # References
//
//   - Decision record 0023: docs/research/decisions/0023-ddm-adapters-and-wire-contract.md
//   - Decision record 0025: docs/research/decisions/0025-reference-server-roles-and-container.md
//   - Plan of record: docs/research/implementation_plan.md (phase 5, phase 8)
//   - Threat model: docs/security/threat-model.md (trust boundaries 5 and 6)
//   - E2E scenarios: docs/testing/e2e-scenarios.md (E2E-010, E2E-015)
//   - Container: Dockerfile, scripts/testdb.sh (ddm-up)
package app
