// Package app composes the reference server's protocol services, storage,
// workers and administrative routes.
//
// # Design
//
// Build validates configuration and assembles one server with MDM and a local
// declarative management engine. Authentication, Cedar authorization, durable
// event capture, and administrative audit share this composition. The reusable
// proxy adapters are available to embedders; this executable does not expose
// separate MDM and DDM deployment roles.
// Administrative families depend on available components and credentials. Run
// supervises workers, and Close drains event delivery and releases owned
// resources.
//
// Enrollment, push, principal/policy storage, audit, certificate revocation and
// quotas are configured here. Both this composition and service.Core deny
// changed-certificate re-enrollment by default. TLS termination, CA/secret storage,
// admission policy and replica routing belong to the deployment.
//
// # References
//
//   - Decision record 0023: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0023-ddm-adapters-and-wire-contract.md
//   - Decision record 0025: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0025-reference-server-roles-and-container.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (trust boundaries 5 and 6)
//   - E2E scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-010, E2E-015)
//   - Container: https://github.com/deploymenttheory/go-apple-dm/blob/main/Dockerfile
//   - Test databases: https://github.com/deploymenttheory/go-apple-dm/blob/main/scripts/testdb.sh
package app
