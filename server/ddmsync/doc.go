// Package ddmsync drives a declarative management engine against a
// server: it turns pending changes into DeclarativeManagement commands and
// pushes, and clears an enrollment's declarative state when the device
// checks out or re-enrols.
//
// # Why
//
// Declarations, sets, membership, tokens, and status are protocol: they are
// what Apple documents, and package ddm holds them with no persistence of
// its own beyond the Store interface it declares. Waking a device is not.
// The notifier needs the command queue to enqueue a DeclarativeManagement
// kick and the retry schedule that a NotNow answer implies, and the
// check-out hook needs the enrollment store to find a device's user
// channels.
//
// Keeping both in ddm meant that importing a declaration type imported the
// command queue with it, which put the engine in the server tier for two
// files out of sixteen (decision record 0044). They live here instead, so
// ddm is the protocol and this package is what a server does with it.
//
// The split follows the shape of the problem rather than the shape of the
// files: Engine.ClearEnrollment stays in ddm, because forgetting an
// enrollment's assignments is a statement about declarative state and needs
// only the engine's own store. ServiceHook is here because deciding when to
// call it means reading the check-in path and the enrollment records.
//
// Changes are drained in groups per enrollment so a burst of assignments
// becomes one command and one push, and a group that fails is left pending
// rather than dropped (KMFDDM #11, decision record 0022).
//
// # References
//
//   - Decision record 0020: docs/research/decisions/0020-ddm-engine-membership-and-storage.md
//   - Decision record 0022: docs/research/decisions/0022-change-notifier.md
//   - Decision record 0039: docs/research/decisions/0039-ddm-is-an-extension-of-mdm.md
//   - Decision record 0044: docs/research/decisions/0044-repository-layout.md
//   - Plan of record: docs/research/implementation_plan.md (phase 6)
//   - Threat model: docs/security/threat-model.md (declarative state on re-enrollment)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementcommand
package ddmsync
