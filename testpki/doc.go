// Package testpki creates ephemeral certificate authorities and identities for
// tests and the device simulator.
//
// # Design
//
// Shared fixtures provide device signing identities, TLS server certificates and
// APNs-topic certificates with consistent chain structure. Keys are generated at
// test time and validity is short. These helpers do not apply production
// issuance policy and their roots must not be trusted outside tests. Production
// signing uses pki/ca with configured keys, policy and storage.
//
// # References
//
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers
package testpki
