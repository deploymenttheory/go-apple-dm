// Package storetest provides a content-cache persistence contract suite for
// shared state backends.
//
// # Design
//
// Run accepts two state stores sharing the same backing storage and checks
// visibility across instances, enrollment binding, pagination, credential
// rotation and revocation, report expiry, and hashed credential storage.
// The caller owns backend setup and cleanup.
//
// # References
//
//   - Persistence implementation: devicemanagement/contentcache/store.go (relative to repository root)
//   - State contract: devicemanagement/state (relative to repository root)
package storetest
