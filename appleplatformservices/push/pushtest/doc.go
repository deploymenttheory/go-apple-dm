// Package pushtest provides a scripted push.Pusher and an in-process APNs
// server.
//
// # Design
//
// Fake records targets and returns configured results for service and notifier
// tests. Server exposes a TLS HTTP/2 endpoint with per-token scripts and a
// default successful response. Tests can exercise invalid tokens, rate limits,
// request rejections and transport behavior without contacting APNs. These
// helpers do not establish live-device delivery.
//
// # References
//
//   - Decision record 0007: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0007-apns-push.md
//   - Decision record 0015: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0015-push-cert-store.md
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-007)
//   - Apple: https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns
//   - Apple: https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens
package pushtest
