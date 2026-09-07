// Package pushnotify resolves stored enrollment push information and topic
// certificates for notification delivery.
//
// # Design
//
// Notifier looks up channel-specific targets, calls push.Pusher and publishes
// classified outcomes. StoreCertStore caches topic credentials and checks
// version changes on expiry of its TTL. Failed reloads return errors. Expiry
// queries support operator-managed renewal schedules.
//
// PushTokenInvalid and PushRejected remain distinct because an inactive token
// and a topic/certificate configuration error require different handling. The
// transport-independent vocabulary stays in appleplatformservices/push; APNs
// acceptance does not establish device delivery.
//
// # References
//
//   - Decision record 0007: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0007-apns-push.md
//   - Decision record 0015: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0015-push-cert-store.md
//   - Decision record 0042: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0042-push-failure-classification.md
//   - Decision record 0044: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0044-repository-layout.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (push certificate rows)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device
package pushnotify
