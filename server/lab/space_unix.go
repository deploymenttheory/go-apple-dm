//go:build !windows

package lab

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// freeBytes reports the space available to the calling user at path.
func freeBytes(path string) (uint64, error) {
	var fs unix.Statfs_t
	if err := unix.Statfs(path, &fs); err != nil {
		return 0, wrapError(err)
	}
	// Bsize is int64 on Linux and uint32 on Darwin, so the block size is range-checked
	// before it is used as an unsigned multiplier.
	if fs.Bsize <= 0 {
		return 0, fmt.Errorf("%w: the filesystem reported no block size", errOperation)
	}
	return fs.Bavail * uint64(fs.Bsize), nil
}
