// Package axmtest provides an in-process Apple Business Manager and Apple School
// Manager API fake.
//
// # Design
//
// The fake verifies ES256 assertions, enforces bearer authentication, serves
// modeled JSON:API resources and cursor pages, and advances assignment
// activities with configurable consistency delays. Scripted authentication,
// rate-limit and server failures let client tests assert retries and error
// handling without live Apple services. Recorded requests expose paths, queries
// and bodies for assertions. The fake models only the API behavior required by
// the client tests.
//
// # References
//
//   - Decision record 0030: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0030-apple-business-manager-api-client.md
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-021)
//   - Apple: https://developer.apple.com/documentation/apple-school-and-business-manager-api/implementing-oauth-for-the-apple-school-manager-and-apple-business-api
//   - Apple: https://developer.apple.com/documentation/applebusinessapi
//   - Apple: https://developer.apple.com/documentation/applebusinessapi/errorresponse
//   - Apple: https://developer.apple.com/documentation/applebusinessapi/paginginformation
//   - Apple: https://developer.apple.com/documentation/applebusinessapi/create-an-orgdeviceactivity
//   - RFC 7519 (JSON Web Token): https://www.rfc-editor.org/rfc/rfc7519
//   - RFC 6749 (OAuth 2.0): https://www.rfc-editor.org/rfc/rfc6749
package axmtest
