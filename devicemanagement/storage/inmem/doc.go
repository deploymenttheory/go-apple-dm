// Package inmem implements a mutex-protected MDM store for tests and
// development.
//
// # Design
//
// The backend follows storage/storagetest contracts for lifecycle, commands,
// tokens, certificate history and migration without a database driver. One mutex
// coordinates access, and records are not sealed. Data is lost when the process
// exits. Use a persistent backend when enrollment state must survive restarts;
// page behavior is contract-compatible without an indexed SQL cost model.
//
// # References
//
//   - Decision record 0005: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0005-storage-interfaces.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package inmem
