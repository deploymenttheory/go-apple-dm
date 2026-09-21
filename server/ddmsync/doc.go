// Package ddmsync enqueues declarative synchronization commands and clears
// declarative state on enrollment lifecycle events.
//
// # Design
//
// Notifier drains persistent changes by enrollment, coalesces recent edits,
// builds DeclarativeManagement commands with tokens and calls the normal service
// enqueue path. Failed work retains attempts and retry state; pushes and events
// are separate from device acknowledgement. Kick reduces polling latency without
// making the engine depend on dispatch.
//
// ServiceHook uses enrollment storage to find dependent user channels and calls
// Engine.ClearEnrollment on initial or changed-identity Authenticate and on
// CheckOut. Same-certificate retries and controlled replacement preserve DDM
// state. Complete returns cleanup failures before the check-in reports success.
// Shared SQL compositions roll back the enrollment mutation and cleanup together;
// independent stores do not provide a distributed transaction. Built-in stores
// classify retries under their write lock. Legacy stores without that result use
// a pre-read fallback and require external serialization for concurrent requests.
//
// # References
//
//   - Decision record 0020: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0020-ddm-engine-membership-and-storage.md
//   - Decision record 0022: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0022-change-notifier.md
//   - Decision record 0039: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0039-ddm-is-an-extension-of-mdm.md
//   - Decision record 0044: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0044-repository-layout.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (declarative state on re-enrollment)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementcommand
package ddmsync
