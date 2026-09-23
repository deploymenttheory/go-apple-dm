//go:build windows

package lab

// freeBytes is unavailable on Windows; the lab's container adapter is macOS-hosted.
func freeBytes(string) (uint64, error) { return 0, errOperation }
