// Package filesystem stores application packages beneath a private filesystem root.
//
// # Design
//
// New opens a traversal-resistant os.Root. Put stages and syncs a private file,
// then publishes it through an atomic hard link without replacing an existing
// object. Open supports bounded reads, and Delete is idempotent. The caller owns
// directory permissions, disk capacity and the lifetime of the root handle.
// Import streams a local source through applications.Manager for the same
// signature and readback checks used by other upload sources.
//
// # References
//
//   - https://pkg.go.dev/os#Root
package filesystem
