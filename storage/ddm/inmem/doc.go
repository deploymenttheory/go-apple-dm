// Package inmem is the reference ddm.Store: a mutex-protected map store
// whose behaviour the contract suite in server/ddmstore/ddmtest defines.
//
// # Why
//
// The engine, its adapters, and the simulator's DDM client all need a
// ddm.Store that is always compiled and needs no database, and the
// contract suite needs a backend simple enough to be obviously right.
// Phase 5 of the plan of record delivers this one with the engine
// (decision record 0020). Update deep-copies the state, runs the callback
// against the copy, and swaps the copy in only when the callback succeeds,
// which gives the transactional semantics the suite demands without a
// transaction log.
//
// It is not persistent. Deployments use ddm/sqlstore, which passes the same
// suite.
//
// # References
//
//   - Decision record 0020: docs/research/decisions/0020-ddm-engine-membership-and-storage.md
//   - Plan of record: docs/research/implementation_plan.md (section 4, DDM engine; phase 5)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - Schema: third_party/device-management/mdm/checkin/declarativemanagement.yaml
package inmem
