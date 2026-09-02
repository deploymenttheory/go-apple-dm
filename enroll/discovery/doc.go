// Package discovery serves the account-driven enrollment service discovery
// endpoint, GET /.well-known/com.apple.remotemanagement, that routes a
// device to the enrollment server for its model family and user
// identifier.
//
// # Why
//
// Account-driven enrollment starts when a person types user@domain on the
// device. The device fetches /.well-known/com.apple.remotemanagement from
// the domain with two query parameters, model-family and user-identifier,
// and expects a JSON document naming the enrollment server and whether it
// performs a user (mdm-byod) or a device (mdm-adde) enrollment. Phase 6 of
// the plan of record adds that endpoint (decision record 0028, claim 1).
//
// This package owns the wire format and the HTTP contract only: exact
// parsing of the model family, the Router hook that decides the answer,
// the JSON body exactly as Apple documents it, GET and HEAD, 405 for other
// methods, the 403 com.apple.well-known.failed rejection as JSON or plist
// by the request's Accept header, and a redirect helper that re-attaches
// the query parameters Apple drops when it follows a redirect. Every
// BaseURL a Router returns is checked to be an absolute https URL before
// it is served. It deliberately leaves out the enrollment endpoint the
// BaseURL points at (enroll/accountdriven), the Apple fallback discovery
// assignment (dep, record 0026), and any policy about which user goes
// where, which lives in the caller's Router.
//
// # References
//
//   - Decision record 0028: docs/research/decisions/0028-account-driven-enrollment-and-service-discovery.md
//   - Decision record 0026: docs/research/decisions/0026-dep-client-sync-and-assignment.md
//   - Plan of record: docs/research/implementation_plan.md (phase 6)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/get-.well-known-com.apple.remotemanagement
//   - Apple: https://developer.apple.com/documentation/devicemanagement/wellknown
//   - Apple: https://developer.apple.com/documentation/devicemanagement/implementing-the-simple-authentication-account-driven-enrollment-flow
//   - Apple: https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment
//   - Schema: third_party/device-management/mdm/errors/well-known.failed.yaml
//   - RFC 9110 (HTTP Semantics, Accept and 405): https://www.rfc-editor.org/rfc/rfc9110
package discovery
