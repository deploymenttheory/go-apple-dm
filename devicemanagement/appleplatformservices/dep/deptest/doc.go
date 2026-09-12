// Package deptest provides a device enrollment service fake, store contract
// suites and failure injection.
//
// # Design
//
// The fake verifies OAuth 1.0a requests and scripts session rotation, cursor
// expiry, repeated pages, throttling, assignment outcomes and token-file
// exchange. Request recording allows tests to inspect exact protocol values.
// Shared store suites cover accounts, atomic keypair promotion, device records,
// cursor timestamps and assignment retries across in-memory and SQL backends.
// The fake models the service behavior used by this client; it is not a complete
// Apple service emulator.
//
// # References
//
//   - Decision record 0026: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0026-dep-client-sync-and-assignment.md
//   - E2E scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-011)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/authenticating-for-automated-device-enrollment
//   - Apple: https://developer.apple.com/documentation/devicemanagement/fetch-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sync-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/assign-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/define-profile
//   - RFC 5849 (OAuth 1.0): https://www.rfc-editor.org/rfc/rfc5849
package deptest
