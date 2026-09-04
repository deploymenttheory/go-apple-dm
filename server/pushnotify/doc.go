// Package pushnotify wakes enrolled devices: it resolves push info from
// storage, sends through a push.Pusher, and serves the APNs certificate a
// topic needs from the push certificate store.
//
// # Why
//
// Sending a push and knowing who to send it to are different jobs with
// different dependencies. The vocabulary of a push -- Target, Result,
// Outcome, the Pusher and CertStore interfaces -- is protocol shaped and
// belongs beside the APNs client that implements it. Resolving an
// enrollment id to a device token, and a topic to a certificate, means a
// database.
//
// Package push held both, so apns imported push for the Pusher interface
// and inherited a dependency on storage through it: a program that only
// wanted to send a notification acquired the persistence layer to do it
// (decision record 0044). The vocabulary stays in push, the storage-backed
// half is here, and apns depends on neither.
//
// Notifier publishes PushTokenInvalid and PushRejected separately on
// purpose. A dead token means one enrollment is gone; a refusal is usually
// a property of the topic, certificate, or environment, and so is usually
// true of every device at once. Collapsing them lets one misconfiguration
// read as a fleet that has gone silent (decision record 0042).
//
// # References
//
//   - Decision record 0007: docs/research/decisions/0007-apns-push.md
//   - Decision record 0015: docs/research/decisions/0015-push-cert-store.md
//   - Decision record 0042: docs/research/decisions/0042-push-failure-classification.md
//   - Decision record 0044: docs/research/decisions/0044-repository-layout.md
//   - Plan of record: docs/research/implementation_plan.md (phase 5)
//   - Threat model: docs/security/threat-model.md (push certificate rows)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device
package pushnotify
