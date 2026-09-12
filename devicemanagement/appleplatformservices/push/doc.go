// Package push defines notification targets, results, certificate sources and
// coalescing for MDM pushes.
//
// # Design
//
// Pusher sends notifications for supplied targets, CertStore resolves topic
// credentials, and Coalescer combines repeated pushes within a configured
// window. Result.Outcome distinguishes acceptance, an inactive token, a rejected
// request and retryable failure. This vocabulary lets callers apply retry and
// notification policy without depending on a database.
//
// The APNs implementation is appleplatformservices/push/apns. Enrollment and
// persisted-certificate lookup live in server/pushnotify; certificate parsing
// lives in pki/pushcert. StaticCertStore supports fixed credentials, and
// pushtest provides scripted implementations.
//
// # References
//
//   - Decision record 0044: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0044-repository-layout.md
//   - Decision record 0007: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0007-apns-push.md
//   - Decision record 0015: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0015-push-cert-store.md
//   - Decision record 0042: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0042-push-failure-classification.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (Push rows)
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-006, E2E-007)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers
//   - Apple: https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens
//   - Schema: third_party/device-management/mdm/checkin/tokenupdate.yaml (Topic, PushMagic, Token)
package push
