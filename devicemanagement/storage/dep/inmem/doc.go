// Package inmem implements a mutex-protected in-memory device enrollment service
// store.
//
// # Design
//
// Update applies changes to copied state and commits only after a successful
// callback. Device pages and cursors and staged keypair promotion are atomic.
// WithKeyring seals OAuth secrets, sessions and private keys in memory through
// storage/crypt. The dep/deptest contracts define behavior shared with
// server/depstore/sqlstore. Encryption does not make this backend persistent;
// all records are lost when the process exits.
//
// # References
//
//   - Decision record 0026: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0026-dep-client-sync-and-assignment.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/device-assignment
//   - Apple: https://developer.apple.com/documentation/devicemanagement/device
package inmem
