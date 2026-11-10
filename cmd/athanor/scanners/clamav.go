// ClamAV adapter (M4-T3). Shells out to `clamdscan`
// (preferred — fast, daemon-backed) and falls back to
// `clamscan` (slow, on-demand) when `clamdscan` is not
// on PATH. The adapter implements Driver; the
// scanners.go helper handles the subprocess contract.
//
// # Absent-binary behavior
//
// When neither `clamdscan` nor `clamscan` is on PATH, the
// adapter's `Available()` returns false. The airlock
// registry's "absent scanner" handling converts this to
// a `VerdictUncertain` verdict with reason
// `scanner:clamav:absent` so the file is quarantined.
// This is the fail-closed posture ADR-0015 requires.
package scanners

import (
	"context"
	"os/exec"

	"github.com/tcs76321/athanor/internal/airlock/scanner"
)

// ClamAV is the ClamAV external-binary adapter.
type ClamAV struct {
	// Binary is the path to the `clamdscan` (or
	// fallback `clamscan`) binary. The zero value
	// means "use exec.LookPath on each Available() /
	// Scan() call." The daemon config can override
	// this for hardened hosts where the binary is in
	// a non-standard path; a follow-up commit wires
	// the config plumbing.
	Binary string
}

// Name is "clamav".
func (c *ClamAV) Name() string { return "clamav" }

// Available reports whether `clamdscan` or `clamscan`
// is on PATH (or whether c.Binary is non-empty).
func (c *ClamAV) Available() bool {
	if c.Binary != "" {
		return true
	}
	for _, name := range []string{"clamdscan", "clamscan"} {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	return false
}

// binary returns the resolved path to the scanner
// binary, preferring clamdscan (daemon-backed) over
// clamscan (on-demand). The result is empty when no
// binary is on PATH.
func (c *ClamAV) binary() string {
	if c.Binary != "" {
		return c.Binary
	}
	if _, err := exec.LookPath("clamdscan"); err == nil {
		return "clamdscan"
	}
	if _, err := exec.LookPath("clamscan"); err == nil {
		return "clamscan"
	}
	return ""
}

// Scan runs clamd[scan] against the input. A clean
// exit + empty stdout is VerdictClean; anything else
// (non-zero exit, non-empty stdout, timeout) is
// VerdictRejected with the scanner output in Details.
func (c *ClamAV) Scan(ctx context.Context, in scanner.ScanInput) (scanner.ScanResult, error) {
	bin := c.binary()
	if bin == "" {
		return scanner.ScanResult{
			Verdict: scanner.VerdictUncertain,
			Reason:  "scanner:clamav:absent",
		}, nil
	}
	// --no-summary: don't print the trailing summary line. --infected: print
	// ONLY infected matches, so a clean scan has empty stdout. The shared
	// runner reads "exit 0 + empty stdout" as clean; --stdout would print
	// "stdin: OK" on every clean scan and reject all content (found against
	// a real source page in the Core Pod).
	return runCommand(ctx, c.Name(), bin,
		[]string{"--no-summary", "--infected", "-"}, in)
}
