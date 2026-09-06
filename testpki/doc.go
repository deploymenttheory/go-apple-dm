// Package testpki generates throwaway certificate authorities and device
// identities for tests and the device simulator.
//
// # Why
//
// Nearly every package in the library needs a certificate at test time: an
// enrollment identity to sign a check-in with, a CA to chain it to, a push
// certificate whose subject UID carries an APNs topic, or a server
// certificate for an in-process TLS listener. Generating them once here
// keeps the fixtures consistent across enroll, httpapi, push, service,
// storage, the simulator, and the end-to-end suite, and keeps private keys
// out of the repository, as the plan of record's test strategy requires.
//
// Nothing here is meant for production: keys are ephemeral, validity is
// short, and there is no policy. Real issuance goes through ca and scep.
//
// # References
//
//   - Plan of record: docs/research/implementation_plan.md (section 6, test strategy; phase 2)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers
package testpki
