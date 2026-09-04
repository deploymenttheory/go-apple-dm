// Package pushtest provides a scripted push.Pusher and an in-process APNs
// server so push behaviour is testable without Apple.
//
// # Why
//
// The interesting push paths are the failures: a 410 that must invalidate
// a token, a 429 that must back off, a certificate that expired, a burst
// that must coalesce. None of them can be provoked against the real APNs
// from a unit test. Fake records every push and returns scripted results
// for the service and notifier tests; Server is a TLS HTTP/2 endpoint that
// answers by token according to a Script, with 200 for anything unscripted,
// so appleplatformservices/push/apns and the end-to-end suite exercise the real client against
// Apple's documented status codes (decision record 0007).
//
// The package asserts nothing itself and holds no production code.
//
// # References
//
//   - Decision record 0007: docs/research/decisions/0007-apns-push.md
//   - Decision record 0015: docs/research/decisions/0015-push-cert-store.md
//   - Plan of record: docs/research/implementation_plan.md (phase 3)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-007)
//   - Apple: https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns
//   - Apple: https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens
package pushtest
