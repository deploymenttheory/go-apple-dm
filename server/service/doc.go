// Package service implements enrollment lifecycle, authorization and command
// delivery behind MDM endpoints.
//
// # Design
//
// Core handles Authenticate, TokenUpdate, CheckOut, certificate pins/reuse, user
// channels and command results. Storage interfaces support interchangeable
// backends. Hooks can observe or veto operations, and typed events report
// outcomes. Optional handlers implement GetToken, UserAuthenticate,
// DeclarativeManagement and ReturnToService.
//
// Certificate status checking, when configured, precedes hooks and device side
// effects independently of pin mode. The library permits re-enrollment by
// default; the reference server denies changed identities unless enabled.
// Command-target checks use available OS/channel metadata but assume unrecorded
// supervision, ADE and user-approved MDM state.
//
// An unconfigured ReturnToService handler answers disabled. An enabled response
// receives the stored bootstrap token if available and not supplied by policy;
// without one, the device can erase fully without app preservation.
//
// # References
//
//   - Decision record 0004: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0004-checkin-and-command-core.md
//   - Decision record 0005: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0005-storage-interfaces.md
//   - Decision record 0014: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0014-cert-association-history.md
//   - Decision record 0016: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0016-user-authenticate-state.md
//   - Decision record 0023: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0023-ddm-adapters-and-wire-contract.md (DMHandler)
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package service
