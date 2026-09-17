// Package osversion represents and compares Apple operating system versions.
//
// # Design
//
// Version retains major, minor and patch components, including older macOS
// versions such as 10.15.4. Its zero value means unspecified. Named macOS major
// constants can be combined with New to express precise release floors without
// assuming that release numbers are consecutive.
//
// The package has no schema or protocol dependencies. Feature availability,
// removal boundaries and enrollment requirements belong to schema/support;
// the device's observed version supplies the comparison target.
//
// # References
//
//   - Availability metadata: devicemanagement/schema/support (relative to repository root)
//   - Mixed-OS fleets: docs/research/decisions/0052-mixed-os-fleets.md (relative to repository root)
package osversion
