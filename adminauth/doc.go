// Package adminauth holds the admin principals and scoped API tokens that
// authenticate callers of the reference server's admin API.
//
// # Why
//
// Phase 5 shipped the admin API behind one static bearer token, which was
// proportionate to the four routes it then had (decision record 0025). Phase 8
// gives that API enrollment inventory, command enqueue, push certificate
// upload, and enrollment export, at which point a single credential both
// erases fleets and exfiltrates FileVault escrow. This package is the
// least-privilege answer: named principals, coarse scopes, and tokens that are
// checksummed, stored only as a digest, and revocable without a restart.
//
// It is deliberately not an identity system. There are no users, sessions,
// passwords, or federation here, and there is no policy language. Those are
// product concerns, and the library refuses them the same way record 0011
// refused Vault and KMS clients and record 0027 refused SAML: the reference
// server ships this implementation, and an integrator who needs mTLS, OIDC, or
// a policy engine supplies their own authorizer instead. Every reference MDM
// server surveyed for record 0034 has one shared secret with no principal, no
// scope, and no revocation; the two that model authorization properly, Fleet
// and Zentral, are whole products rather than libraries.
//
// # References
//
//   - Decision record: docs/research/decisions/0034-admin-api-and-authorization.md
//   - Decision record: docs/research/decisions/0035-mdmctl-structure-and-credentials.md
//   - Plan of record: docs/research/implementation_plan.md (phase 8)
//   - Threat model: docs/security/threat-model.md (admin API, repudiation)
//   - E2E scenarios: docs/testing/e2e-scenarios.md (E2E-024)
//   - Apple documents nothing about administering an MDM server; the device-facing
//     protocol is elsewhere. The prior art is catalogued in
//     docs/research/reference_projects.md and read in record 0034.
//   - RFC 6750: bearer token usage, including the WWW-Authenticate challenge
//   - RFC 9110 section 11: the 401 and 403 distinction the API relies on
package adminauth
