// Package recovery creates encrypted server checkpoints and verifies them
// before restoring into an empty deployment.
//
// Backup requires a maintenance pause acknowledged by every writer. Checkpoints
// combine a consistent SQL snapshot, setup configuration and the original
// storage keys in an authenticated age archive. Verification checks the archive,
// file manifest and encrypted database values in private temporary directories.
// Callers must close verified or prepared checkpoints to remove those files.
//
// Restoration requires an absent destination directory and an empty database.
// Schema definitions come from compiled migrations rather than SQL supplied by
// the archive. Restored deployments retain the write pause and are never started
// automatically. Operators must stop the original processes before using the
// restored deployment and explicitly resume writes when checks are complete.
//
// See the recovery guide for backup, verification and restore procedures:
// https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/recovery.md
package recovery
