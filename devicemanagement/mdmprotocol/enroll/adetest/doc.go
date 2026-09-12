// Package adetest constructs signed MachineInfo and its request carriers for
// Automated Device Enrollment tests.
//
// # Design
//
// NewChain creates a test root, an expired SHA-1 intermediate shaped like the
// Apple device CA, and a leaf. Sign supports authenticated-attribute and
// content-only CMS forms; Request builds header, query and body carriers. These
// fixtures exercise verification and compatibility behavior without using real
// device identities. Test anchors must be confined to tests.
//
// # References
//
//   - Decision record 0027: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-011, E2E-018)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/machineinfo
//   - Apple: https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views
//   - Schema: third_party/device-management/other/machineinfo.yaml
//   - RFC 5652 (Cryptographic Message Syntax): https://www.rfc-editor.org/rfc/rfc5652
package adetest
