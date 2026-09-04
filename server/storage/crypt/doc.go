// Package crypt seals the per-device secrets a storage backend must retain
// on Apple's behalf with AES-256-GCM under a named key from a
// secrets.Provider.
//
// # Why
//
// The check-in protocol hands the server an UnlockToken in TokenUpdate and
// a BootstrapToken in SetBootstrapToken, and the push certificate store
// holds private keys; a copy of the database alone must not expose them.
// Phase 4 of the plan of record adds this package (decision record 0013):
// a Keyring with one active key and any number of retired ones, additional
// authenticated data that binds every blob to its table, column, and row
// id so a ciphertext cannot be moved to another row, and a Strict mode
// that refuses plaintext rows. The key name travels in the ciphertext
// header, which lets an operator rotate by naming a new active key while
// keeping the retired key in the accepted list until every stored value
// has been rewrapped.
//
// Which columns are sealed, and the Rewrap that walks them, live in the
// SQL backends through sqlcommon. This package knows only bytes, keys, and
// AAD.
//
// # References
//
//   - Decision record 0011: docs/research/decisions/0011-secrets-provider.md
//   - Decision record 0013: docs/research/decisions/0013-secrets-at-rest.md
//   - Plan of record: docs/research/implementation_plan.md (phase 4)
//   - Threat model: docs/security/threat-model.md (Storage disclosure and ciphertext row swap rows)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
//   - Schema: third_party/device-management/mdm/checkin/tokenupdate.yaml (UnlockToken)
//   - Schema: third_party/device-management/mdm/checkin/setbootstraptoken.yaml (BootstrapToken)
package crypt
