// Package pushcert parses APNs certificate/key pairs and derives their push
// topic.
//
// # Design
//
// Apple encodes the topic in the certificate subject UID, which must match the
// enrollment's Topic. Parse uses the standard library to check certificate/key
// pairing and exposes the certificate validity period. Storage can validate
// uploads through this leaf package without importing notification orchestration
// or database drivers. Sending notifications belongs to
// appleplatformservices/push/apns, and persisted credential lookup to
// server/pushnotify.
//
// # References
//
//   - Decision record 0007: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0007-apns-push.md
//   - Decision record 0044: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0044-repository-layout.md
//   - Decision record 0015: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0015-push-cert-store.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (wrong or expired push certificate row)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
//   - Schema: third_party/device-management/mdm/checkin/authenticate.yaml, tokenupdate.yaml (Topic)
package pushcert
