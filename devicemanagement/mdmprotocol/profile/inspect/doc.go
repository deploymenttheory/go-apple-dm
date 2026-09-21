// Package inspect checks configuration profiles without contacting a device.
//
// # Design
//
// Inspect combines the existing profile parser, generated validators and platform
// support metadata. Inspection of the original payload keys reports unknown
// content that a typed decode could otherwise discard. Findings identify paths
// and rules without printing configuration values.
//
// CMS signature integrity and certificate trust are separate results. Encrypted
// or unknown content is reported as unvalidated. A clean report establishes
// schema conformance for the supplied target, not successful installation. The
// CLI owns input/output formats and maps report outcomes to its exit codes.
//
// # References
//
//   - Inspection guide: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/status-and-profile-inspection.md
//   - Apple profile structure: https://developer.apple.com/documentation/devicemanagement/configuring-multiple-devices-using-profiles
//   - Apple schema metadata: https://github.com/apple/device-management/blob/release/docs/schema.md
package inspect
