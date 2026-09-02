// Package attesttest mints Managed Device Attestation chains that look like
// Apple's, for tests and for the device simulator.
//
// # Why
//
// Real attestations come from Apple's servers and cover real hardware, so
// nothing in this repository can produce one. Everything that exercises the
// attestation path needs a stand-in: the verifier's own tests, the ACME
// server's tests, the device simulator, and the end-to-end scenarios. This
// package is that stand-in. It builds a root and an intermediate of its own
// and issues leaves carrying the same extensions in the same encodings that
// Apple uses, so a test that passes here would pass against a real chain
// for the same reasons.
//
// It is a test helper, not a security boundary: its anchors must never be
// configured on a server facing real devices, which is why they are
// supplied explicitly rather than added to the package defaults.
//
// # References
//
//   - Decision record 0032: docs/research/decisions/0032-managed-device-attestation.md
//   - Plan of record: docs/research/implementation_plan.md (phase 7)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/deviceinformationresponse
//   - Schema: third_party/device-management/mdm/commands/information.device.yaml
package attesttest
