// Package ddmtest is the contract every ddm.Store backend must satisfy:
// suites a backend's own test runs through RunAll with a constructor
// returning a fresh, empty store, fixture helpers, and a Failing wrapper
// that injects errors by method name, inside transactions too.
//
// # Why
//
// The DDM engine's correctness rests on storage semantics Apple's protocol
// makes visible to devices: a token that does not change when nothing
// changed, a revision that does when something did, cascading deletes
// when a declaration or set goes away, membership as the union of a
// device's sets, a status report that replaces the previous one
// atomically, queued changes for the notifier, and rollback when an Update
// callback fails (decision records 0020, 0021, and 0022). Phase 5 of the
// plan of record pins each of those here once, so ddm/inmem and
// ddm/sqlstore are held to the same rule and a divergence is a failing
// test. Failing hands the callback a wrapped transaction so engine error
// paths can be exercised without a broken database.
//
// The suites pin behaviour, not the wire protocol; the four endpoints and
// token derivation are tested in ddm itself.
//
// # References
//
//   - Decision record 0020: docs/research/decisions/0020-ddm-engine-membership-and-storage.md
//   - Decision record 0021: docs/research/decisions/0021-status-reports-and-subscriptions.md
//   - Decision record 0022: docs/research/decisions/0022-change-notifier.md
//   - Plan of record: docs/research/implementation_plan.md (section 4, DDM engine; phase 5)
//   - Threat model: docs/security/threat-model.md (/status row, retention)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - Apple: https://developer.apple.com/documentation/devicemanagement/status-items
//   - Schema: third_party/device-management/declarative/protocol/*.yaml
//   - Schema: third_party/device-management/mdm/checkin/declarativemanagement.yaml
package ddmtest
