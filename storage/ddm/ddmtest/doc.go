// Package ddmtest defines transactional declaration-store contracts and
// fixtures.
//
// # Design
//
// RunAll covers declarations, versioning, sets, assignments, snapshots, status,
// pending changes, pagination, cascade behavior and rollback. A Failing wrapper
// injects errors inside transactions as well as store calls. In-memory and SQL
// backends run the same suite. Wire response semantics and token derivation are
// tested separately in mdmprotocol/ddm.
//
// # References
//
//   - Decision record 0020: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0020-ddm-engine-membership-and-storage.md
//   - Decision record 0021: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0021-status-reports-and-subscriptions.md
//   - Decision record 0022: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0022-change-notifier.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (/status row, retention)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - Apple: https://developer.apple.com/documentation/devicemanagement/status-items
//   - Schema: third_party/device-management/declarative/protocol/*.yaml
//   - Schema: third_party/device-management/mdm/checkin/declarativemanagement.yaml
package ddmtest
