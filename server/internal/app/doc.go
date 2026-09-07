// Package app composes the reference server's protocol services, storage,
// workers and administrative routes.
//
// # Design
//
// Build validates configuration and assembles mdm, ddm or all roles. The MDM
// role serves devices and uses a local declaration engine or authenticated
// proxyclient; the DDM role serves the engine through proxyserver.
// Administrative families depend on available components and credentials. Run
// supervises workers, and Close drains event delivery and releases owned
// resources.
//
// Enrollment, push, principal/policy storage, audit, certificate revocation and
// quotas are configured here. Library defaults can differ from this composition,
// including re-enrollment policy. TLS termination, durable CA/secret material,
// admission policy and replica routing belong to the deployment.
//
// # References
//
//   - Decision record 0023: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0023-ddm-adapters-and-wire-contract.md
//   - Decision record 0025: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0025-reference-server-roles-and-container.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (trust boundaries 5 and 6)
//   - E2E scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-010, E2E-015)
//   - Container: Dockerfile, scripts/testdb.sh (ddm-up)
package app
