// Package ddm is the Declarative Device Management engine: declarations
// and their canonical form, sets and membership, per-enrollment manifests
// and synchronisation tokens, status reports, and the change notifier.
//
// # Why
//
// Apple's declarative management moves policy evaluation onto the device:
// the server publishes declarations and a token that summarises them, the
// device pulls what changed and reports status back. Phase 5 of the plan of
// record adds that engine on top of the MDM core. This package owns the
// protocol semantics (tokens, the four endpoints, 404-means-remove, empty
// status responses, full-report replacement) and the storage contract, and
// leaves transport to the ddm/adapter packages and persistence to ddm/inmem
// and ddm/sqlstore. It relies on nothing from NanoMDM or MicroMDM; their
// behaviour is cited in the decision records only as evidence.
//
// # References
//
//   - Decision record 0019: docs/research/decisions/0019-canonical-json-and-ddm-tokens.md
//   - Decision record 0020: docs/research/decisions/0020-ddm-engine-membership-and-storage.md
//   - Decision record 0021: docs/research/decisions/0021-status-reports-and-subscriptions.md
//   - Decision record 0022: docs/research/decisions/0022-change-notifier.md
//   - Plan of record: docs/research/implementation_plan.md (phase 5)
//   - Threat model: docs/security/threat-model.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - Apple: https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations
//   - Apple: https://developer.apple.com/documentation/devicemanagement/status-items
//   - Schema: third_party/device-management/declarative/protocol/*.yaml
//   - Schema: third_party/device-management/declarative/declarations/**, declarative/status/**
//   - Schema: third_party/device-management/mdm/checkin/declarativemanagement.yaml
//   - RFC 8785 (JSON Canonicalization Scheme): https://www.rfc-editor.org/rfc/rfc8785
package ddm
