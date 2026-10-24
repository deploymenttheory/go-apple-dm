// Package axm provides a typed client for the Apple Business Manager and Apple
// School Manager APIs.
//
// # Design
//
// The client exchanges ES256 assertions for cached bearer tokens, coordinates
// refresh, retries an API 401 once and applies bounded backoff to retryable
// failures. Lists return a single page; opt-in iterators follow links.next with
// a page cap. Activity helpers validate request rules and wait for completion
// and assignment convergence. Serial-based helpers first enumerate and resolve
// actual organization resources with ResolveOrgDevicesBySerial; endpoint paths
// and NewActivityRequest use returned opaque resource IDs. No inventory model
// is required.
//
// Types are maintained from Apple's documentation. The package does not persist
// credentials; server/axmcreds supplies sealed persistence for the reference
// server. The axmtest package provides an independent fake for modeled endpoints
// and failures.
//
// Activity logs accept the configured API origin or the documented HTTPS
// store.blobstore.apple.com origin. Presigned storage requests have no API bearer
// or cookies, reject redirects, and buffer bounded CSV with sanitized errors.
// FetchActivityLog validates transport and CSV structure, not device success.
// VerifyActivityLog checks a caller-supplied column schema against the intended
// serial set, retaining per-device failures and unknown columns. A completed
// activity, a complete log and successful outcomes are separate facts.
//
// API and token endpoint overrides require absolute HTTPS URLs with no user
// information or fragment. Custom transports own TLS verification policy.
//
// # References
//
//   - Decision record 0030: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0030-apple-business-manager-api-client.md
//   - Decision record 0011: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0011-secrets-provider.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
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
