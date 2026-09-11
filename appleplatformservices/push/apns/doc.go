// Package apns implements certificate-authenticated APNs HTTP/2 clients for
// MDM wake-ups and ordinary app alert/background notifications.
//
// # Design
//
// Requests post an mdm push-magic body to /3/device/<token> using the enrollment
// topic and a TLS client certificate. Client maintains a connection pool per
// topic, reloads changed certificates and rejects expired credentials. Classify
// maps APNs responses to push outcomes: only 410 marks a token invalid, while
// request rejections and retryable failures remain distinct. RetryAfter reflects
// the response header when valid.
//
// Client implements push.Pusher; NewApp returns a separate app sender. Close
// cancels active requests and retires connections, as does certificate rotation.
// Enrollment lookup, certificate-store
// caching and event publication belong to server/pushnotify. APNs acceptance
// does not establish device delivery or command execution.
//
// Host overrides require absolute HTTPS URLs with no user information or
// fragment, checked before loading push credentials.
//
// # References
//
//   - Decision record 0007: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0007-apns-push.md
//   - Decision record 0015: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0015-push-cert-store.md
//   - Decision record 0042: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0042-push-failure-classification.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (Push rows)
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-007)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers
//   - Apple: https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns
//   - Apple: https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens
//   - Apple: https://developer.apple.com/documentation/usernotifications/handling-notification-responses-from-apns
package apns
