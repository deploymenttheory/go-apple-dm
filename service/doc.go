// Package service implements the MDM server behaviour behind the check-in
// and command endpoints.
//
// # Why
//
// The HTTP layer decodes and authenticates; this package decides. It owns
// the enrollment lifecycle (Authenticate, TokenUpdate, CheckOut, user
// channels), identity pinning and re-enrollment policy, command delivery
// with NotNow backoff, the optional message handlers (GetToken,
// UserAuthenticate, DeclarativeManagement through service.DMHandler), and
// the hooks and events that let integrators observe or veto every step.
// Storage is behind the storage interfaces so the same core runs on every
// backend, and the DDM engine plugs in through the handler and hook seams
// rather than being imported.
//
// # References
//
//   - Decision record 0004: docs/research/decisions/0004-checkin-and-command-core.md
//   - Decision record 0005: docs/research/decisions/0005-storage-interfaces.md
//   - Decision record 0014: docs/research/decisions/0014-cert-association-history.md
//   - Decision record 0016: docs/research/decisions/0016-user-authenticate-state.md
//   - Decision record 0023: docs/research/decisions/0023-ddm-adapters-and-wire-contract.md (DMHandler)
//   - Plan of record: docs/research/implementation_plan.md (phase 2)
//   - Threat model: docs/security/threat-model.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package service
