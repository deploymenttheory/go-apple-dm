// Package pushcert parses APNs push certificates and derives their topic.
//
// # Why
//
// Apple puts the push topic in the certificate's subject UID attribute
// (OID 0.9.2342.19200300.100.1.1); it always starts with "com.apple.mgmt",
// and the Topic a device reports in Authenticate and TokenUpdate must
// match it. Phase 4 of the plan of record stores push certificates in the
// database (decision record 0015), so both the storage backends and the
// push package need to prove that a PEM pair is a valid certificate with
// its matching key and to learn its topic before accepting it.
//
// This package imports only the standard library on purpose: storage
// backends import it to validate uploaded push certificates, and push
// imports storage, so any dependency on push would create an import
// cycle. Sending pushes is push and push/apns.
//
// # References
//
//   - Decision record 0007: docs/research/decisions/0007-apns-push.md
//   - Decision record 0015: docs/research/decisions/0015-push-cert-store.md
//   - Plan of record: docs/research/implementation_plan.md (phase 4)
//   - Threat model: docs/security/threat-model.md (wrong or expired push certificate row)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
//   - Schema: third_party/device-management/mdm/checkin/authenticate.yaml, tokenupdate.yaml (Topic)
package pushcert
