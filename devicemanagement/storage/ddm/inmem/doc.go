// Package inmem implements a mutex-protected in-memory ddm.Store.
//
// # Design
//
// Update deep-copies state, applies the callback and commits the copy only on
// success. This gives declarations, membership, status and change rows the same
// transaction boundary defined by storage/ddm/ddmtest. Returned data does not
// expose mutable store internals. State is process-local and lost on restart;
// server/ddmstore/sqlstore provides persistent backends.
//
// # References
//
//   - Decision record 0020: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0020-ddm-engine-membership-and-storage.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - Schema: third_party/device-management/mdm/checkin/declarativemanagement.yaml
package inmem
