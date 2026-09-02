// Package axm is a client for the Apple Business Manager and Apple School
// Manager APIs: OAuth client-assertion authentication, every documented
// endpoint as a typed method, explicit pagination, and the device
// assignment workflows built on org device activities.
//
// # Why
//
// An MDM server that enrolls devices through Automated Device Enrollment
// has to tell Apple Business Manager which devices belong to it, and the
// modern way to do that is this API rather than the legacy DEP token flow.
// Phase 6 of the plan of record adds the client (decision record 0030).
// Authentication is an ES256 client assertion built with crypto/ecdsa,
// exchanged for a bearer token that is cached, renewed ahead of expiry
// under a singleflight, and replaced once after a 401 with the request
// replayed. Responses are decoded into hand-written types from Apple's
// documentation with unknown members kept; errors decode Apple's JSON:API
// error document with both source forms; 429 honours Retry-After and 5xx
// backs off with jitter. List calls return one Page and the iterators
// follow links.next under a page cap. The workflows enforce Apple's local
// rules (server presence per activity type, migration deadline within 90
// days) and tolerate the eventual consistency of the assignment linkage.
//
// The package does not persist tokens, run a proxy, or generate its types;
// go-sdk-appleservices and nanoaxm are read-only references and never
// imported. The fake for tests is axm/axmtest.
//
// # References
//
//   - Decision record 0030: docs/research/decisions/0030-apple-business-manager-api-client.md
//   - Decision record 0011: docs/research/decisions/0011-secrets-provider.md
//   - Decision record 0013: docs/research/decisions/0013-secrets-at-rest.md
//   - Plan of record: docs/research/implementation_plan.md (phase 6)
//   - Apple: https://developer.apple.com/documentation/apple-school-and-business-manager-api/implementing-oauth-for-the-apple-school-manager-and-apple-business-api
//   - Apple: https://developer.apple.com/documentation/applebusinessapi
//   - Apple: https://developer.apple.com/documentation/appleschoolmanagerapi
//   - Apple: https://developer.apple.com/documentation/applebusinessapi/create-an-orgdeviceactivity
//   - Apple: https://developer.apple.com/documentation/applebusinessapi/errorresponse
//   - Apple: https://developer.apple.com/documentation/applebusinessapi/paginginformation
//   - Apple: https://developer.apple.com/documentation/applebusinessapi/pageddocumentlinks
//   - RFC 7519 (JSON Web Token): https://www.rfc-editor.org/rfc/rfc7519
//   - RFC 7523 (JWT client authentication): https://www.rfc-editor.org/rfc/rfc7523
//   - RFC 6749 (OAuth 2.0): https://www.rfc-editor.org/rfc/rfc6749
package axm
