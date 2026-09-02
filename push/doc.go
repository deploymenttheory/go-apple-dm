// Package push wakes managed devices through APNs: a Pusher sends one MDM
// push per Target, Notifier looks targets up in storage, sends, and
// publishes events, Coalescer collapses bursts, and CertStore supplies the
// push certificate per topic.
//
// # Why
//
// An MDM server cannot talk to a device; it can only ask APNs to wake it,
// after which the device connects and asks for work. Phase 3 of the plan of
// record needs that wake-up path to be reliable under bursts and honest
// about failure: a 410 from APNs marks the token invalid and publishes
// PushTokenInvalid instead of retrying forever, a burst of changes for one
// enrollment becomes one push, and a rotated push certificate is picked up
// without a restart (decision records 0007 and 0015). StoreCertStore
// reloads from storage.PushCertStore on version change; StaticCertStore is
// for tests and single-tenant deployments.
//
// The HTTP/2 client that actually talks to Apple is push/apns, certificate
// parsing is push/pushcert (standard library only, to avoid an import
// cycle with storage), and fakes are push/pushtest.
//
// # References
//
//   - Decision record 0007: docs/research/decisions/0007-apns-push.md
//   - Decision record 0015: docs/research/decisions/0015-push-cert-store.md
//   - Plan of record: docs/research/implementation_plan.md (phase 3)
//   - Threat model: docs/security/threat-model.md (Push rows)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-006, E2E-007)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers
//   - Apple: https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens
//   - Schema: third_party/device-management/mdm/checkin/tokenupdate.yaml (Topic, PushMagic, Token)
package push
