// Package ddm implements declarations, membership, snapshots, synchronization
// tokens and status handling for declarative device management.
//
// # Design
//
// The engine serves Apple's four endpoint operations over a transactional
// storage contract. Canonical content determines tokens, per-enrollment
// snapshots preserve advertised versions, and full status reports replace stored
// state atomically. Device and user channels have independent membership.
// Resolver and Expander hooks support dynamic assignments and per-enrollment
// content.
//
// Persistence implementations live in storage/ddm and server/ddmstore. Transport
// adapters live in server/ddmadapter. Transactional change rows are drained by
// server/ddmsync, which also supplies lifecycle cleanup hooks. The engine does
// not dispatch commands or pushes directly.
//
// # References
//
//   - Decision record 0019: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0019-canonical-json-and-ddm-tokens.md
//   - Decision record 0020: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0020-ddm-engine-membership-and-storage.md
//   - Decision record 0021: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0021-status-reports-and-subscriptions.md
//   - Decision record 0022: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0022-change-notifier.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - Apple: https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations
//   - Apple: https://developer.apple.com/documentation/devicemanagement/status-items
//   - Schema: third_party/device-management/declarative/protocol/*.yaml
//   - Schema: third_party/device-management/declarative/declarations/**, declarative/status/**
//   - Schema: third_party/device-management/mdm/checkin/declarativemanagement.yaml
//   - RFC 8785 (JSON Canonicalization Scheme): https://www.rfc-editor.org/rfc/rfc8785
package ddm
