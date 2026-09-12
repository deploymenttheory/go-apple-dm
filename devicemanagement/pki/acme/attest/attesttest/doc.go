// Package attesttest generates Managed Device Attestation test chains and
// property extensions.
//
// # Design
//
// Test authorities issue leaves using the modeled Apple property encodings so
// verifier, ACME and simulator tests can exercise valid and malformed
// attestations without Apple hardware. These chains establish fixture behavior
// only; passing fixture tests does not establish interoperability with real
// device attestations. Test anchors must never be trusted by a deployment
// accepting real devices.
//
// # References
//
//   - Decision record 0032: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0032-managed-device-attestation.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/deviceinformationresponse
//   - Schema: third_party/device-management/mdm/commands/information.device.yaml
package attesttest
