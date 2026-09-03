// Package adminauthtest is the contract suite every adminauth.Store must
// pass, plus a failing store for error-path tests.
//
// # Why
//
// The in-memory store and the three SQL backends have to behave identically,
// or an authorization decision would depend on which database a deployment
// chose. RunSuite is what proves that, the same way storagetest, ddmtest,
// deptest, and acmetest do for their own interfaces.
//
// The cases that matter most are the ones a naive implementation gets wrong: a
// revoked principal must not be findable by an empty digest, a rotated token
// must stop working the moment the new one is issued, and the policy version
// must move on every write so a cached compilation notices.
//
// # References
//
//   - Decision record: docs/research/decisions/0034-admin-api-and-authorization.md
//   - Plan of record: docs/research/implementation_plan.md (phase 8)
//   - Sibling suites: storage/storagetest, ddm/ddmtest, acme/acmetest
package adminauthtest
