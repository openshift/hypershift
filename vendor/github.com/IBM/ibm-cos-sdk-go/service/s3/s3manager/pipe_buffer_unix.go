//go:build !windows
// +build !windows

// Tag !windows targets all Unix-like platforms since "_unix.go" is not a recognized GOOS suffix by the Go toolchain.

package s3manager

import (
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// getPipeBufferSize returns the kernel pipe buffer size.
// On Linux, queries the actual size via fcntl(F_GETPIPE_SZ).
// On other Unix platforms, returns a 64 KB fallback (F_GETPIPE_SZ is Linux-only).
func getPipeBufferSize(pipe *os.File) int {
	const defaultSize = 64 * 1024

	if runtime.GOOS != "linux" {
		return defaultSize
	}

	// F_GETPIPE_SZ = 1032 (linux/fcntl.h)
	size, err := unix.FcntlInt(pipe.Fd(), 1032, 0)
	if err != nil {
		return defaultSize
	}

	return size
}
