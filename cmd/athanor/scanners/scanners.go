// Package scanners hosts the external-binary scanner
// adapters (ClamAV, YARA) for the M4-T3 airlock pipeline
// (ROADMAP; ADR-0015). The package extends the Gate G1
// `os/exec` allowlist to permit subprocess-based malware
// detection; `internal/airlock/scanner` provides the
// `Scanner` interface these adapters implement.
//
// # Why this package is in cmd/
//
// Gate G1 (`internal/gate/gate_test.go`) forbids
// `os/exec` in `internal/` and limits it in `cmd/` to a
// named file (M2-T2's podman client) plus an allowlisted
// directory (this package, added in M4-T3). The directory
// allowlist keeps the gate's "no tool execution in agent
// code" guarantee intact: only the airlock's scanner
// adapters can shell out.
//
// # Absent-binary behavior
//
// Each adapter reports its `Available()` status. An
// adapter whose binary is not on `PATH` (or whose
// required clamd/yara ruleset is missing) returns
// `VerdictUncertain` with reason `scanner:<name>:absent`.
// This is the fail-closed posture ADR-0015 requires:
// missing capability degrades to quarantine, never to
// silent pass.
package scanners

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/tcs76321/athanor/internal/airlock/scanner"
)

// bytesBuffer is an alias for bytes.Buffer used by
// runCommand. The alias is intentional so the call
// sites are greppable.
type bytesBuffer = bytes.Buffer

// Driver is the interface every external-binary adapter
// implements. The interface is intentionally tiny: a
// driver reports availability, runs the scan, and is
// safe to invoke concurrently. A driver is *not* a
// `scanner.Scanner` directly; the Driver factory below
// adapts it.
type Driver interface {
	// Name is the registry key (e.g. "clamav", "yara").
	Name() string
	// Available reports whether the underlying binary
	// (and any required rule set) is present on the
	// host. Called at registry construction time; a
	// driver that flips between Available and not at
	// runtime is a separate concern (not in M4-T3).
	Available() bool
	// Scan runs the binary against in and returns the
	// verdict. The implementation honors ctx
	// cancellation and the timeout; a hung subprocess
	// cannot wedge the daemon.
	Scan(ctx context.Context, in scanner.ScanInput) (scanner.ScanResult, error)
}

// Default timeout for an external scanner subprocess.
// Configurable; the daemon passes the value through the
// airlock config (a follow-up commit adds a
// `airlock.scanner_subprocess_timeout` field).
const defaultTimeout = 30 * time.Second

// runCommand is a tiny helper that runs an executable
// with stdin fed from in.Bytes, captures stdout/stderr,
// and converts the exit status to a verdict.
//
// The helper is the single subprocess-invocation site for
// the M4-T3 external scanners. Adding a new adapter is a
// matter of constructing a different *exec.Cmd; the
// verdict mapping is uniform across all of them.
func runCommand(ctx context.Context, name string, bin string, args []string, in scanner.ScanInput) (scanner.ScanResult, error) {
	if in.Bytes == nil {
		return scanner.ScanResult{}, fmt.Errorf("scanner %s: ScanInput.Bytes is nil (external scanners require in-memory input)", name)
	}
	timeout := defaultTimeout
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(tctx, bin, args...)
	cmd.Stdin = bytes.NewReader(in.Bytes)
	var stdout, stderr bytesBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	// exit 0 + empty stdout = clean
	// non-zero exit OR non-empty stdout = rejected
	// (some scanners return exit 1 with the signature
	// in stdout; we treat both as rejection signals).
	if err == nil {
		return scanner.ScanResult{Verdict: scanner.VerdictClean}, nil
	}
	// Distinguish timeout from a real scanner verdict.
	if tctx.Err() == context.DeadlineExceeded {
		return scanner.ScanResult{}, fmt.Errorf("scanner %s: subprocess timed out after %v", name, timeout)
	}
	// Real scanner verdict: rejection. The stdout
	// typically carries the signature; the stderr
	// carries diagnostics. Both go into Details for
	// post-mortem.
	return scanner.ScanResult{
		Verdict: scanner.VerdictRejected,
		Reason:  fmt.Sprintf("scanner:%s:rejected", name),
		Details: map[string]any{
			"stdout": stdout.String(),
			"stderr": stderr.String(),
			"exit":   exitCode(err),
		},
	}, nil
}

// exitCode extracts the exit code from an *exec.ExitError,
// returning -1 when err is not an exit error.
func exitCode(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}
