// Package adminauth authenticates administrative principals and evaluates Cedar
// authorization policies.
//
// # Design
//
// Principals have roles and root authority; API tokens are checksummed, stored
// as digests, and revocable. Route actions and request context are evaluated
// under default-deny Cedar policies. Root administration, credential-issuance
// restrictions and last-root protection are enforced outside policy grants.
// Only root may mutate principals or credentials, including self-rotation;
// role subsets do not establish equivalent authority under arbitrary Cedar.
// Store.ApplyPrincipal atomically preserves an active root under concurrent
// credential changes. Natural expiry and policy lockout remain operator concerns.
//
// Managed roles are named records; membership alone grants no authority.
// Policies are schema-validated and inactive documents remain stored. Any
// invalid active policy or evaluation diagnostic denies operational access.
// The reference server exchanges DM_BOOTSTRAP_TOKEN for the first stored root
// credential exactly once. Root can repair authorization independently of Cedar;
// ordinary device operations always need an explicit policy grant.
// This package does not implement users, passwords, sessions or federation.
//
// # References
//
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0034-admin-api-and-authorization.md
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0035-dmctl-structure-and-credentials.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (admin API, repudiation)
//   - E2E scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-024)
//   - Administrative authorization is project policy; decision record 0034 cites the implementation references that inform it.
//   - RFC 6750: bearer token usage, including the WWW-Authenticate challenge
//   - RFC 9110 section 11: the 401 and 403 distinction the API relies on
package adminauth
