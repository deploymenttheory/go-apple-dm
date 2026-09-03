// Package apns is the APNs HTTP/2 client for MDM pushes.
//
// # Why
//
// An MDM push is a POST to /3/device/<token> with the JSON body
// {"mdm": "<PushMagic>"} and the apns-topic header set to the enrollment's
// topic, authenticated with the push certificate as the TLS client
// certificate. Phase 3 of the plan of record needs a client that does
// exactly that and turns Apple's status codes and reason strings into
// typed results through Classify: 410 means the token is gone, 429 and 503
// mean back off, the 400 and 403 families mean APNs refused the request
// itself, and anything else may succeed on retry (decision records 0007 and
// 0042). Client
// keeps one HTTP/2 connection pool per topic, reloads when the CertStore
// reports a new certificate, and refuses an expired one rather than
// letting APNs reject every request (decision record 0015).
//
// Deciding whom to push, coalescing, and publishing events are push's
// concern; this package implements push.Pusher and nothing more.
//
// # References
//
//   - Decision record 0007: docs/research/decisions/0007-apns-push.md
//   - Decision record 0015: docs/research/decisions/0015-push-cert-store.md
//   - Decision record 0042: docs/research/decisions/0042-push-failure-classification.md
//   - Plan of record: docs/research/implementation_plan.md (phase 3)
//   - Threat model: docs/security/threat-model.md (Push rows)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-007)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers
//   - Apple: https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns
//   - Apple: https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens
//   - Apple: https://developer.apple.com/documentation/usernotifications/handling-notification-responses-from-apns
package apns
