// Package push is the vocabulary of an MDM push: a Pusher sends one
// notification per Target and reports a Result, Coalescer collapses bursts,
// and CertStore supplies the push certificate for a topic.
//
// # Why
//
// An MDM server cannot talk to a device; it can only ask APNs to wake it,
// after which the device connects and asks for work. Phase 3 of the plan of
// record needs that wake-up path to be reliable under bursts and honest
// about failure: a 410 from APNs marks the token invalid and publishes
// PushTokenInvalid instead of retrying forever, a burst of changes for one
// enrollment becomes one push, and a rotated push certificate is picked up
// without a restart (decision records 0007 and 0015).
//
// This package holds only the vocabulary and the parts that need nothing but
// it, so that the APNs client can implement Pusher without acquiring a
// database: resolving an enrollment to a device token, and a topic to a
// stored certificate, is pushnotify's job (decision record 0044).
// StaticCertStore is here because a fixed map needs no storage, and serves
// tests and single-tenant deployments.
//
// Result.Outcome is what a caller acts on. It separates a token APNs says
// is dead (410, and only 410) from a request APNs refused — a wrong topic,
// a mismatched or expired certificate, the sandbox environment — because
// the second is normally true of every device at once and must not be read
// as a fleet that has gone quiet (decision record 0042).
//
// The HTTP/2 client that actually talks to Apple is appleplatformservices/push/apns, certificate
// parsing is pushcert (standard library only, so storage can validate an
// uploaded certificate without depending on push), and fakes are
// appleplatformservices/push/pushtest.
//
// # References
//
//   - Decision record 0044: docs/research/decisions/0044-repository-layout.md
//   - Decision record 0007: docs/research/decisions/0007-apns-push.md
//   - Decision record 0015: docs/research/decisions/0015-push-cert-store.md
//   - Decision record 0042: docs/research/decisions/0042-push-failure-classification.md
//   - Plan of record: docs/research/implementation_plan.md (phase 3)
//   - Threat model: docs/security/threat-model.md (Push rows)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-006, E2E-007)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers
//   - Apple: https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens
//   - Schema: third_party/device-management/mdm/checkin/tokenupdate.yaml (Topic, PushMagic, Token)
package push
