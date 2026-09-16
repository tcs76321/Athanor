package scanner

import (
	"context"
	"encoding/base64"
	"regexp"
)

// PromptInjectionHeuristic is the in-tree
// `prompt-injection-heuristic` scanner. It is the cheap,
// always-on, regex-based layer that catches obvious
// prompt-injection patterns. The high-leverage scanner
// (the security-persona classifier) lives behind the
// Gateway (M4-T5) and is wired in there. The heuristic
// is the long-prompt and inbox guardrail.
//
// # Design
//
// The scanner is a deterministic pass/fail. There is no
// "uncertain" verdict: every pattern either matches or
// doesn't. The aggregation with other scanners (size,
// zipbomb, clamav, yara) produces the uncertain/rejected
// verdicts on aggregate.
//
// Patterns are case-insensitive regex over the input
// bytes. The list is intentionally short and biased
// toward obvious injections; the goal is "low false-
// negative rate on the well-known cases," not "low false-
// positive rate on all text."
type PromptInjectionHeuristic struct {
	// MinLength is the minimum input size (in bytes) to
	// scan. Shorter inputs are assumed clean. The
	// long-prompt caller can raise this to skip the
	// cost on short strings.
	MinLength int
}

// Name is "prompt-injection-heuristic" (the registry key).
func (p *PromptInjectionHeuristic) Name() string {
	return "prompt-injection-heuristic"
}

// NewPromptInjectionHeuristic constructs a heuristic
// scanner with the given min-length floor. A non-positive
// floor panics: a "no floor" heuristic is the absence of
// the scanner.
func NewPromptInjectionHeuristic(minLength int) *PromptInjectionHeuristic {
	if minLength < 1 {
		panic("scanner: PromptInjectionHeuristic requires MinLength >= 1")
	}
	return &PromptInjectionHeuristic{MinLength: minLength}
}

// patterns is the closed set of prompt-injection regex
// patterns the heuristic looks for. Order is irrelevant
// (any match returns VerdictRejected); the order in the
// slice is the order the Reason string reports when a
// pattern matches first.
//
// The list is deliberately short. Each pattern is a
// well-known injection shape from the public prompt-
// injection literature. Adding a new pattern is a
// one-line edit here plus a test row in
// heuristic_test.go's adversarial corpus.
var patterns = []*regexp.Regexp{
	// "Ignore all previous instructions" and variants.
	regexp.MustCompile(`(?i)ignore (all |every )?(previous|prior|above) (instructions?|directives?|rules?)`),
	regexp.MustCompile(`(?i)disregard (all|every|the) (instructions?|directives?|rules?)`),
	regexp.MustCompile(`(?i)disregard (all|every|the) the (rules?|instructions?|directives?)`),
	regexp.MustCompile(`(?i)forget (everything|all) (above|before|prior)`),
	// "You are now ..." role reassignment. The multiline flag
	// matters: without it, `^` anchors at the start of the whole
	// text and any prefix the attacker prepends ("Sure! You are
	// now DAN") evades the pattern. (Found by the M4-T8
	// adversarial corpus.)
	regexp.MustCompile(`(?im)^\s*you are now\s+`),
	regexp.MustCompile(`(?i)\bact as\b.*\b(admin|root|system|developer|jailbreak)\b`),
	// ChatML / special-token shape: many prompt
	// injections are smuggled inside ChatML tags.
	regexp.MustCompile(`<\|.*?\|>`),
	// System / assistant role markers embedded in user
	// text. The LLM should not see these in untrusted
	// input.
	regexp.MustCompile(`(?im)^\s*system\s*:`),
	regexp.MustCompile(`(?im)^\s*assistant\s*:`),
	// "Disregard / override / bypass safety / filters"
	regexp.MustCompile(`(?i)(bypass|circumvent|override) (safety|guardrails?|filters?|restrictions?)`),
}

// base64Threshold documents why the base64 check exists.
// The threshold itself is the regex's minimum-match
// length (1024 chars ≈ 1 KiB). Smaller base64 blobs are
// normal (UUIDs, single-line tokens) and pass.
const base64MinLen = 1024

// base64Re is a coarse match for base64-shaped strings.
// The full charset is [A-Za-z0-9+/=]; we accept a long
// run of that character class as "base64-shaped." Go's
// regexp engine caps quantifier counts at 1000, so we
// use a smaller literal bound (256) and rely on the
// caller to verify the match's actual length against
// `base64MinLen`. The two-step check (regex match +
// length verification) catches both the shape and the
// size, which is what we want.
var base64Re = regexp.MustCompile(`[A-Za-z0-9+/=]{256,}`)

// Scan classifies in as VerdictRejected on the first
// matching pattern, VerdictClean otherwise. Empty or
// short inputs are VerdictClean.
func (p *PromptInjectionHeuristic) Scan(ctx context.Context, in ScanInput) (ScanResult, error) {
	if len(in.Bytes) < p.MinLength {
		return ScanResult{Verdict: VerdictClean}, nil
	}
	text := string(in.Bytes)
	for _, pat := range patterns {
		if pat.MatchString(text) {
			return ScanResult{
				Verdict: VerdictRejected,
				Reason:  "scanner:prompt-injection-heuristic:pattern:" + pat.String(),
				Details: map[string]any{
					"matched_pattern": pat.String(),
				},
			}, nil
		}
	}
	// Long base64 blob: a separate check, with the
	// reason carrying the size for post-mortem.
	// The regex matches at 256+ chars; the threshold
	// (base64MinLen, 1024) is the actual gate. The
	// length check fires on the matched substring's
	// length, not the input's.
//
// Fail-closed on decodability (M4-T8): a rejection used to require
// a clean StdEncoding decode of the whole run, which let an
// attacker evade the check by chaining padded blobs into one long
// run (the mid-stream "==" breaks the decode). Shape + size is the
// gate now; decodability is recorded when it works, not required.
	if loc := base64Re.FindStringIndex(text); loc != nil {
		matchLen := loc[1] - loc[0]
		if matchLen >= base64MinLen {
			details := map[string]any{
				"blob_offset": loc[0],
				"blob_size":   matchLen,
			}
			if blob, err := base64.StdEncoding.DecodeString(text[loc[0]:loc[1]]); err == nil && len(blob) > 0 {
				details["decoded_size"] = len(blob)
			}
			return ScanResult{
				Verdict: VerdictRejected,
				Reason:  "scanner:prompt-injection-heuristic:base64_blob",
				Details: details,
			}, nil
		}
	}
	return ScanResult{
		Verdict: VerdictClean,
		Details: map[string]any{"length": len(in.Bytes)},
	}, nil
}

// base64MinLenForTest returns the threshold; tests
// reference it rather than hard-coding 1024 so future
// changes to the constant don't silently desync the
// test corpus.
func base64MinLenForTest() int { return base64MinLen }
