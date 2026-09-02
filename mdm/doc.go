// Package mdm is the protocol core of the Apple MDM check-in and command
// channels: enrollment identity, request context, check-in message
// decoding, command envelopes, and command response decoding.
//
// # Why
//
// Apple documents the check-in messages and the commands as YAML, but the
// structure around them only in prose: which of UDID and EnrollmentID
// identifies a device, user, or Shared iPad user channel; the CommandUUID
// and Command envelope a queued command travels in; the Status,
// ErrorChain, and per-command result a device sends back; the Push fields
// and Enrollment record the service must retain. Phase 2 of the plan of
// record needs those semantics once, typed, and free of transport, so the
// service, storage, and httpapi packages can share them (decision record
// 0004).
//
// Wire types come from the generated schema packages; this package adds
// the envelope structure and the decoders that dispatch on MessageType and
// RequestType through the schema/checkin and schema/commands registries.
// It stores nothing and serves nothing: persistence is storage, state
// machines are service, and HTTP is httpapi.
//
// # References
//
//   - Decision record 0002: docs/research/decisions/0002-plist-library.md
//   - Decision record 0004: docs/research/decisions/0004-checkin-and-command-core.md
//   - Decision record 0006: docs/research/decisions/0006-mdm-signature-verification.md (request identity)
//   - Decision record 0016: docs/research/decisions/0016-user-authenticate-state.md (UserAuthenticate responses)
//   - Plan of record: docs/research/implementation_plan.md (section 3, core domain model; phase 2)
//   - Threat model: docs/security/threat-model.md (/checkin and /connect rows)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-001 to E2E-005)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/commands-and-queries
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
//   - Schema: third_party/device-management/mdm/checkin/*.yaml
//   - Schema: third_party/device-management/mdm/commands/*.yaml
package mdm
