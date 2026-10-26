// Package privatefile creates and checks private credential and evidence paths.
//
// # Design
//
// Creation helpers restrict access before returning a writable file. On Unix,
// files receive owner-only permissions. On Windows, an explicit access control
// list grants access to the current user, LocalSystem and administrators.
// Protection is applied to the opened file even if its path is renamed.
//
// Mkdir exclusively creates a directory with owner-only mode 0700 on Unix and
// the same protected Windows ACL at creation. It creates no parents and changes
// no existing path. Existing parents retain their current access policy.
// Windows directory creation supports long local and UNC paths without changing
// system settings; already-qualified and device paths retain their namespace.
//
// Check rejects nonregular files and permissions that grant access to other
// users. CheckDirectory applies the same permission contract to directories and
// rejects nondirectories. OpenFile callers must request exclusive creation;
// OpenRootFile retains the caller's os.Root boundary. SyncDirectory flushes
// directory metadata on Unix and validates the handle on Windows, where directory
// flushing is not supported. Callers must sync file contents separately.
//
// # References
//
//   - Recovery: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/recovery.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md
package privatefile
