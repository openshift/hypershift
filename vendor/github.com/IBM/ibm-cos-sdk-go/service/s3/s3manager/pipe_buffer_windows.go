// Build tag is omitted since the "_windows.go" suffix automatically targets Windows in the Go toolchain.

package s3manager

import "os"

// getPipeBufferSize returns a fixed fallback on Windows.
// fcntl(F_GETPIPE_SZ) is Linux-only; the value is used as the write chunk
// size in orderedWriter and 64 KB is a safe default for streaming I/O.
func getPipeBufferSize(_ *os.File) int {
	return 64 * 1024
}
