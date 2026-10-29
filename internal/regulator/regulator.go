// Package regulator is the ADR-0090 evaluation-checkpoint decision: settle,
// refine a unit, reframe the approach, probe, defer, or abandon. It is pure and
// deterministic so the same analysis and failure always produce the same
// decision; the engine supplies the state and performs the action.
package regulator

import (
	"hash/fnv"
	"sort"
	"strings"
	"unicode"

	"github.com/tcs76321/athanor/internal/analysis"
)

// Decision is the regulator's move at the checkpoint.
type Decision string

const (
	// Settle means the unit's falsifier passed; stop spending on it.
	Settle Decision = "settle"
	// Refine raises one unit to deliberate and retries it.
	Refine Decision = "refine"
	// Reframe changes the approach because the same failure repeated.
	Reframe Decision = "reframe"
	// Probe runs the cheapest check before spending deliberation.
	Probe Decision = "probe"
	// Defer keeps state and resumes when the horizon budget replenishes.
	Defer Decision = "defer"
	// Abandon fails with a reason: effort or frame is genuinely exhausted.
	Abandon Decision = "abandon"
)

// Input is the complete pure input to Decide.
type Input struct {
	Units             []analysis.Unit
	Implicated        string         // unit id, "" when frame-level
	Signature         string         // this failure's signature
	PreviousSignature string         // the prior failure's signature
	Refined           map[string]int // unit id → refine count
	Iterations        int
	MaxIterations     int
	BudgetExhausted   bool
}

// Result is the chosen move and its reason.
type Result struct {
	Decision Decision
	UnitID   string
	Reason   string
}

// Decide implements the checkpoint policy. Budget exhaustion defers before any
// quality judgement (running out of night is not a failed job). A repeated
// signature outranks a first refine: more thinking inside the same frame has
// low expected value, so the frame changes instead.
func Decide(in Input) Result {
	if in.BudgetExhausted {
		return Result{Decision: Defer, Reason: "horizon budget exhausted"}
	}
	if in.Iterations >= in.MaxIterations {
		return Result{Decision: Abandon, Reason: "reflection budget exhausted"}
	}
	if in.Implicated != "" && in.Refined[in.Implicated] == 0 {
		return Result{Decision: Refine, UnitID: in.Implicated,
			Reason: "failure attributed to an unrefined unit"}
	}
	if in.Signature != "" && in.Signature == in.PreviousSignature {
		return Result{Decision: Reframe, Reason: "the same failure repeated"}
	}
	if in.Implicated != "" {
		return Result{Decision: Reframe, UnitID: in.Implicated,
			Reason: "the unit was already refined"}
	}
	if in.Signature == "" {
		return Result{Decision: Probe, Reason: "failure reason is uninformative"}
	}
	return Result{Decision: Probe, Reason: "first distinct frame-level failure; gather evidence"}
}

// Attribute maps a failure text to the unit whose goal/falsifier it names. It
// returns "" when no unit has a clear match, which the regulator treats as a
// frame-level failure (the safe default).
func Attribute(units []analysis.Unit, failure string) string {
	words := tokens(failure)
	if len(words) == 0 {
		return ""
	}
	best, bestScore := "", 0
	for _, u := range units {
		unitWords := tokens(u.Goal + " " + u.Reason + " " + u.Falsifier)
		score := 0
		for w := range unitWords {
			if words[w] {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = u.ID, score
		}
	}
	if bestScore < 2 {
		return ""
	}
	return best
}

// Signature is a stable hash of the normalized failure reasons. Order is
// irrelevant: the inputs are sorted first.
func Signature(reasons []string) string {
	norm := make([]string, 0, len(reasons))
	for _, r := range reasons {
		n := normalize(r)
		if n != "" {
			norm = append(norm, n)
		}
	}
	if len(norm) == 0 {
		return ""
	}
	sort.Strings(norm)
	h := fnv.New64a()
	for _, n := range norm {
		_, _ = h.Write([]byte(n))
		_, _ = h.Write([]byte{'\n'})
	}
	return hex(h.Sum64())
}

func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func tokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(f) >= 4 {
			out[f] = true
		}
	}
	return out
}

func hex(v uint64) string {
	const digits = "0123456789abcdef"
	var b [16]byte
	for i := 15; i >= 0; i-- {
		b[i] = digits[v&0xf]
		v >>= 4
	}
	return string(b[:])
}
