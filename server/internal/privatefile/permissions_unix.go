//go:build !windows

package privatefile

import "os"

// Protect applies owner-only access to a newly created file.
func Protect(path string) error { return wrap(os.Chmod(path, 0o600)) }

func protectFile(file *os.File) error { return wrap(file.Chmod(0o600)) }

// Check refuses nonregular files and access granted to other users.
func Check(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return wrap(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return wrap(os.ErrPermission)
	}
	return nil
}

// SyncDirectory flushes directory metadata after exclusive publication.
func SyncDirectory(file *os.File) error { return wrap(file.Sync()) }
