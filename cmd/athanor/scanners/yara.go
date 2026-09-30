// YARA adapter (M4-T3). Shells out to the `yara` CLI
// against a baseline rule file shipped under
// `state/yara/`. The adapter implements Driver; the
// scanners.go helper handles the subprocess contract.
//
// # Absent-binary / missing-rule-set behavior
//
// When `yara` is not on PATH OR the configured rule set
// (default `state/yara/injection.yar`) is not readable,
// the adapter's `Available()` returns false. The airlock
// registry's "absent scanner" handling converts this to
// a `VerdictUncertain` verdict with reason
// `scanner:yara:absent` so the file is quarantined.
// This is the fail-closed posture ADR-0015 requires.
package scanners

import (
	"context"
	"os"
	"os/exec"

	"github.com/tcs76321/athanor/internal/airlock/scanner"
)

// YARA is the YARA external-binary adapter.
type YARA struct {
	// RuleSet is the path to the YARA rule file the
	// daemon ships. The default is
	// "state/yara/injection.yar"; a hardened host
	// with a private rule set can override via
	// config.
	RuleSet string
}

// Name is "yara".
func (y *YARA) Name() string { return "yara" }

// Available reports whether `yara` is on PATH AND the
// configured rule set is readable. The rule-set check
// is the structural guarantee that the subprocess
// invocation will find a non-empty ruleset file.
func (y *YARA) Available() bool {
	if _, err := exec.LookPath("yara"); err != nil {
		return false
	}
	rs := y.RuleSet
	if rs == "" {
		rs = "state/yara/injection.yar"
	}
	info, err := os.Stat(rs)
	if err != nil {
		return false
	}
	return !info.IsDir() && info.Size() > 0
}

// ruleSet returns the resolved rule-set path. The
// default is `state/yara/injection.yar`; the daemon
// config overrides this for hardened hosts.
func (y *YARA) ruleSet() string {
	if y.RuleSet == "" {
		return "state/yara/injection.yar"
	}
	return y.RuleSet
}

// Scan runs `yara` against the rule set. A clean exit
// (no matches) is VerdictClean; a non-zero exit (any
// match) is VerdictRejected with the rule name in
// Details. The subprocess reads the rule set from disk
// and the input bytes from stdin.
func (y *YARA) Scan(ctx context.Context, in scanner.ScanInput) (scanner.ScanResult, error) {
	if _, err := exec.LookPath("yara"); err != nil {
		return scanner.ScanResult{
			Verdict: scanner.VerdictUncertain,
			Reason:  "scanner:yara:absent",
		}, nil
	}
	rs := y.ruleSet()
	info, err := os.Stat(rs)
	if err != nil {
		return scanner.ScanResult{
			Verdict: scanner.VerdictUncertain,
			Reason:  "scanner:yara:ruleset_missing",
		}, nil
	}
	if info.IsDir() || info.Size() == 0 {
		return scanner.ScanResult{
			Verdict: scanner.VerdictUncertain,
			Reason:  "scanner:yara:ruleset_empty",
		}, nil
	}
	// yara -r - reads rules from `rules` and stdin is
	// the file. The exit code is 0 on no match, 1 on
	// match, anything else is an error. The runCommand
	// helper maps non-zero to VerdictRejected.
	return runCommand(ctx, y.Name(), "yara",
		[]string{rs, "-"}, in)
}
