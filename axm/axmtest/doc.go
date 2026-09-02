// Package axmtest is an in-process fake of the Apple Business Manager and
// Apple School Manager APIs: the OAuth token endpoint, every documented
// resource endpoint with JSON:API bodies and cursor pagination, an
// activity engine, and fault injection.
//
// # Why
//
// The interesting behaviour of the axm client cannot be provoked against
// Apple from a unit test: an assertion Apple would reject, a token that
// expired mid-run, a 401 that must be replayed exactly once, a 429 with a
// Retry-After date, a 5xx that must back off, an activity that takes
// minutes and whose assignment linkage lags behind, a 406 for a missing
// Accept header. Server answers all of them by rule (decision record
// 0030): the token endpoint verifies the ES256 assertion against the
// registered public key (kid, aud, iat and exp window, jti uniqueness,
// scope), bearer tokens are enforced with Apple's error document, fields
// selections are honoured and unknown ones rejected with source.parameter,
// activities advance through Apple's sub-statuses on Advance or a timer
// with a configurable consistency lag, downloadUrl serves a CSV, and the
// faults (ExpireTokens, RejectNextTokenRequests, RateLimit, ServerError,
// per-serial outcomes) are one call each. A recorder keeps every request
// so tests can assert paths, queries, and bodies.
//
// The package asserts nothing itself, holds no production code, and does
// not import axm, so the client's own tests can be internal.
//
// # References
//
//   - Decision record 0030: docs/research/decisions/0030-apple-business-manager-api-client.md
//   - Plan of record: docs/research/implementation_plan.md (phase 6)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-021)
//   - Apple: https://developer.apple.com/documentation/apple-school-and-business-manager-api/implementing-oauth-for-the-apple-school-manager-and-apple-business-api
//   - Apple: https://developer.apple.com/documentation/applebusinessapi
//   - Apple: https://developer.apple.com/documentation/applebusinessapi/errorresponse
//   - Apple: https://developer.apple.com/documentation/applebusinessapi/paginginformation
//   - Apple: https://developer.apple.com/documentation/applebusinessapi/create-an-orgdeviceactivity
//   - RFC 7519 (JSON Web Token): https://www.rfc-editor.org/rfc/rfc7519
//   - RFC 6749 (OAuth 2.0): https://www.rfc-editor.org/rfc/rfc6749
package axmtest
