//go:build !windows

package lab

import "golang.org/x/sys/unix"

// freeBytes reports the space available to the calling user at path.
func freeBytes(path string) (uint64, error) {
	var fs unix.Statfs_t
	if err := unix.Statfs(path, &fs); err != nil {
		return 0, wrapError(err)
	}
	return fs.Bavail * uint64(fs.Bsize), nil
}
