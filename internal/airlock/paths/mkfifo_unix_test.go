//go:build !windows

package paths

import (
	"os"
	"path/filepath"
	"syscall"
)

// mkFifo creates a named pipe (FIFO) at the given path on
// Unix-likes via syscall.Mkfifo. The companion paths_test.go
// table relies on Lstat reporting ModeNamedPipe for the
// resulting entry, so the device-row of the table can be
// exercised.
//
// syscall is imported in a _test.go file; Gate G1's
// test-file exemption covers this (the production sources
// in this package — paths.go, paths_linux.go,
// paths_darwin.go — do not import syscall except for the
// O_NOFOLLOW constant under rule 5 of internal/gate).
func mkFifo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return syscall.Mkfifo(path, 0o644)
}
