// Package adminauth authenticates administrative principals and evaluates Cedar
// authorization policies.
//
// # Design
//
// Principals have roles and root authority; API tokens are checksummed, stored
// as digests, and revocable. Route actions and request context are evaluated
// under default-deny Cedar policies. Root administration, credential-issuance
// restrictions and last-root protection are enforced outside policy grants.
//
// The reference server separately accepts DM_ADMIN_TOKEN as a root credential
// that bypasses policy, including when a principal store is configured. Its use
// is audited as break-glass; removal requires unsetting it and restarting. Use
// it to bootstrap principals, then verify their access and remove it. This
// package does not implement administrative users, passwords, sessions or
// federation; applications can supply another authorizer.
//
// # References
//
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0034-admin-api-and-authorization.md
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0035-dmctl-structure-and-credentials.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (admin API, repudiation)
//   - E2E scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-024)
//   - Apple documents nothing about administering an MDM server; the device-facing protocol is elsewhere. The prior art is catalogued in https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/reference_projects.md and read in record 0034.
//   - RFC 6750: bearer token usage, including the WWW-Authenticate challenge
//   - RFC 9110 section 11: the 401 and 403 distinction the API relies on
package adminauth
