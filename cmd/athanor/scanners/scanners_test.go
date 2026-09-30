// Tests for the external-binary scanner adapters. The
// structural tests (absent-binary behavior) run in CI;
// the behavioral tests (real ClamAV / YARA binaries)
// are gated behind `ATHANOR_RUN_INTEGRATION=1` and
// exercise the same `Driver` interface the daemon uses.
// The pattern mirrors M2-T6's `internal/jobpod/security_test.go`.
package scanners

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/airlock/scanner"
)

// TestClamAV_AbsentBinaryFailsClosed: with PATH cleared,
// Available() reports false.
func TestClamAV_AbsentBinaryFailsClosed(t *testing.T) {
	t.Setenv("PATH", "")
	c := &ClamAV{}
	if c.Available() {
		t.Error("ClamAV.Available() = true with PATH empty; want false")
	}
	res, err := c.Scan(context.Background(), scanner.ScanInput{Bytes: []byte("x")})
	if err != nil {
		t.Fatalf("Scan returned err: %v; want nil", err)
	}
	if res.Verdict != scanner.VerdictUncertain {
		t.Errorf("Verdict = %v, want VerdictUncertain (fail-closed)", res.Verdict)
	}
	if res.Reason != "scanner:clamav:absent" {
		t.Errorf("Reason = %q, want scanner:clamav:absent", res.Reason)
	}
}

// TestClamAV_ExplicitBinaryPath: an explicit Binary
// overrides PATH lookup.
func TestClamAV_ExplicitBinaryPath(t *testing.T) {
	t.Setenv("PATH", "")
	c := &ClamAV{Binary: "/usr/bin/clamdscan"}
	if !c.Available() {
		t.Error("ClamAV.Available() = false with explicit Binary")
	}
	if got := c.binary(); got != "/usr/bin/clamdscan" {
		t.Errorf("binary() = %q, want explicit override", got)
	}
}

// TestYARA_AbsentBinaryFailsClosed.
func TestYARA_AbsentBinaryFailsClosed(t *testing.T) {
	t.Setenv("PATH", "")
	y := &YARA{}
	if y.Available() {
		t.Error("YARA.Available() = true with PATH empty")
	}
	res, _ := y.Scan(context.Background(), scanner.ScanInput{Bytes: []byte("x")})
	if res.Verdict != scanner.VerdictUncertain {
		t.Errorf("Verdict = %v, want VerdictUncertain", res.Verdict)
	}
	if res.Reason != "scanner:yara:absent" {
		t.Errorf("Reason = %q, want scanner:yara:absent", res.Reason)
	}
}

// TestYARA_MissingRuleSetFailsClosed: a missing rule set
// makes Available() return false.
func TestYARA_MissingRuleSetFailsClosed(t *testing.T) {
	t.Setenv("PATH", "")
	y := &YARA{RuleSet: "/nonexistent/injection.yar"}
	if y.Available() {
		t.Error("YARA.Available() = true with missing rule set")
	}
	res, _ := y.Scan(context.Background(), scanner.ScanInput{Bytes: []byte("x")})
	if res.Verdict != scanner.VerdictUncertain {
		t.Errorf("Verdict = %v, want VerdictUncertain", res.Verdict)
	}
}

// TestYARA_EmptyRuleSetFailsClosed.
func TestYARA_EmptyRuleSetFailsClosed(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.yar")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	y := &YARA{RuleSet: empty}
	if y.Available() {
		t.Error("YARA.Available() = true with empty rule set")
	}
}

// TestRunCommand_PropagatesVerdict: a real subprocess
// (`true` exits 0; `false` exits 1) is mapped to
// VerdictClean / VerdictRejected by the helper. The
// structural proof that the verdict-mapping logic in
// runCommand is independent of the ClamAV / YARA
// adapter specifics.
func TestRunCommand_PropagatesVerdict(t *testing.T) {
	if _, err := os.Stat("/bin/true"); err != nil {
		t.Skipf("no /bin/true: %v", err)
	}
	if _, err := os.Stat("/bin/false"); err != nil {
		t.Skipf("no /bin/false: %v", err)
	}
	res, err := runCommand(context.Background(), "clean", "/bin/true", nil, scanner.ScanInput{Bytes: []byte("x")})
	if err != nil {
		t.Fatalf("runCommand true: %v", err)
	}
	if res.Verdict != scanner.VerdictClean {
		t.Errorf("runCommand true: Verdict = %v, want VerdictClean", res.Verdict)
	}
	res, err = runCommand(context.Background(), "rej", "/bin/false", nil, scanner.ScanInput{Bytes: []byte("x")})
	if err != nil {
		t.Fatalf("runCommand false: %v", err)
	}
	if res.Verdict != scanner.VerdictRejected {
		t.Errorf("runCommand false: Verdict = %v, want VerdictRejected", res.Verdict)
	}
	if res.Reason != "scanner:rej:rejected" {
		t.Errorf("Reason = %q, want scanner:rej:rejected", res.Reason)
	}
}
