// Package adminauthtest defines adminauth.Store contract tests and a
// failing-store wrapper.
//
// # Design
//
// RunSuite checks principal lookup, token digests, revocation, immediate
// rotation and policy-version changes across backends. The suite keeps
// authorization persistence independent of the selected database. Failure
// injection exercises authentication and administration error paths without
// corrupting a real store.
//
// # References
//
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0034-admin-api-and-authorization.md
//   - Sibling suites: server/storage/storagetest, server/ddmstore/ddmtest, acme/acmetest
package adminauthtest
