// Package target defines the device side of lab acceptance runs.
//
// A Target is where device behavior comes from: the protocol simulator, an operator
// enrolled device, a guestweave virtual machine or a physical Mac reached over SSH. Lab
// modules observe and drive the device only through this contract, so the same module runs
// against any target whose Info and Capabilities satisfy it.
//
// # Design
//
// Describe reports the platform, OS, hardware and enrollment identifiers that a module's
// applicability rules and the report record. Operations a driver cannot perform return
// ErrUnsupported, and Capabilities lists the operations it can, so the runner can report a
// module as unsupported before it starts rather than failing part-way.
//
// # References
//
//   - https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/local_lab_acceptance_testing.md
package target
