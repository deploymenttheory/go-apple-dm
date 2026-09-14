// Package privatefile creates and checks files containing local credentials.
//
// Creation helpers restrict access before returning a writable file. On Unix,
// files receive owner-only permissions. On Windows, an explicit access control
// list grants access to the current user, LocalSystem and administrators.
// Protection is applied to the opened file even if its path is renamed.
//
// Check rejects nonregular files and permissions that grant access to other
// users. OpenFile callers must request exclusive creation; OpenRootFile retains
// the caller's os.Root boundary. SyncDirectory flushes directory metadata on
// Unix and validates the handle on Windows, where directory flushing is not
// supported. Callers must sync file contents separately.
package privatefile
