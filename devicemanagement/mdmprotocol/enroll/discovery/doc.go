// Package discovery serves account-driven enrollment service discovery at
// /.well-known/com.apple.remotemanagement.
//
// # Design
//
// The handler parses model-family and user-identifier, calls Router and
// validates each returned BaseURL as absolute HTTPS. It supports GET and HEAD,
// rejects other methods, and represents routing denial with
// com.apple.well-known.failed. Redirect helpers preserve the discovery query
// parameters.
//
// mdm-byod selects account-driven User Enrollment; mdm-adde selects
// account-driven Device Enrollment. Discovery is public routing information, not
// authentication. The caller owns routing policy, and the accountdriven package
// serves the selected enrollment endpoint.
//
// # References
//
//   - Decision record 0028: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0028-account-driven-enrollment-and-service-discovery.md
//   - Decision record 0026: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0026-dep-client-sync-and-assignment.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/get-.well-known-com.apple.remotemanagement
//   - Apple: https://developer.apple.com/documentation/devicemanagement/wellknown
//   - Apple: https://developer.apple.com/documentation/devicemanagement/implementing-the-simple-authentication-account-driven-enrollment-flow
//   - Apple: https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment
//   - Schema: third_party/device-management/mdm/errors/well-known.failed.yaml
//   - RFC 9110 (HTTP Semantics, Accept and 405): https://www.rfc-editor.org/rfc/rfc9110
package discovery
