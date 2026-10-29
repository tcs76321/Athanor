package onboard

import (
	"encoding/json"
	"reflect"
	"strings"
)

// LadderVersion is the state-file schema. A version bump invalidates old
// measurements rather than silently mixing instruments.
const LadderVersion = 1

// Check is how a generator answer is scored. Both checks are deterministic;
// no judge model is consulted (ADR-0049: qualification is measurement).
type Check string

const (
	// CheckExact normalizes whitespace, case, fences, and a leading
	// "answer:" before comparing.
	CheckExact Check = "exact"
	// CheckJSON compares parsed JSON values, so formatting does not matter.
	CheckJSON Check = "json"
)

// GenCase is one grounded generator case.
type GenCase struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	Want   string `json:"want"`
	Check  Check  `json:"check"`
}

// JudgeCase is one artifact pair with a known better artifact. The labels are
// external and fixed; a model's agreement is the measurement.
type JudgeCase struct {
	ID   string `json:"id"`
	Goal string `json:"goal"`
	Good string `json:"good"`
	Bad  string `json:"bad"`
}

// Ladder is the bounded qualification instrument. Generators and Judges are
// intentionally small: this is a machine-local screen, not the frozen corpus.
type Ladder struct {
	Generators []GenCase
	Judges     []JudgeCase
}

// DefaultLadder is the shipped screen. Generator cases test deterministic
// reasoning and output discipline; judge cases test whether a model can prefer
// a correct artifact over a plausible broken one.
func DefaultLadder() Ladder {
	return Ladder{
		Generators: []GenCase{
			{ID: "gen-arith", Check: CheckExact, Want: "391",
				Prompt: "Compute 17 * 23. Reply with only the integer, no words."},
			{ID: "gen-trace", Check: CheckExact, Want: "01",
				Prompt: "What does this Python print? Reply with only the output.\n\nfor i in range(2):\n    print(i, end=\"\")"},
			{ID: "gen-json", Check: CheckJSON, Want: `{"lang":"go","stable":true}`,
				Prompt: `Extract the language and whether it is stable from: "Go 1.24 is a stable release." ` +
					`Reply with only JSON: {"lang":...,"stable":...}`},
			{ID: "gen-bug", Check: CheckExact, Want: "1",
				Prompt: "How many times does this loop print? Reply with only the integer.\n\n" +
					"for i in range(0, 3, 2):\n    print(i)"},
		},
		Judges: []JudgeCase{
			{
				ID: "judge-edge-case", Goal: "Return the average of a list; an empty list returns 0.",
				Good: "def avg(xs):\n    if not xs:\n        return 0\n    return sum(xs) / len(xs)",
				Bad:  "def avg(xs):\n    return sum(xs) / len(xs)",
			},
			{
				ID: "judge-error-path", Goal: "Read a JSON file and return its parsed value; invalid JSON must raise a clear error.",
				Good: "def load(path):\n    try:\n        with open(path) as f:\n            return json.load(f)\n    except json.JSONDecodeError as e:\n        raise ValueError(f\"invalid JSON in {path}: {e}\") from e",
				Bad:  "def load(path):\n    with open(path) as f:\n        return json.load(f)",
			},
			{
				ID: "judge-sql-rollback", Goal: "An additive migration must be reversible.",
				Good: "-- up\nALTER TABLE t ADD COLUMN age INTEGER;\n-- down\nALTER TABLE t DROP COLUMN age;",
				Bad:  "-- up\nALTER TABLE t ADD COLUMN age INTEGER;",
			},
			{
				ID: "judge-doc-contract", Goal: "Document a function's inputs, outputs, and error behavior.",
				Good: "parse(s) -> dict\n  s: JSON string.\n  Returns the decoded object.\n  Raises ValueError if s is not valid JSON.",
				Bad:  "parse(s)\n  Parses s and returns the result.",
			},
			{
				ID: "judge-no-network", Goal: "A test double must not perform real network I/O.",
				Good: "class FakeClient:\n    def __init__(self, response):\n        self.response = response\n    def get(self, url):\n        return self.response",
				Bad:  "class FakeClient:\n    def get(self, url):\n        return requests.get(url).json()",
			},
			{
				ID: "judge-close-file", Goal: "Read a file and always release the handle.",
				Good: "with open(path) as f:\n    return f.read()",
				Bad:  "f = open(path)\nreturn f.read()",
			},
		},
	}
}

// GenPrompt is the instruction sent for a generator case.
func GenPrompt(c GenCase) string { return c.Prompt }

// JudgePrompt renders the pair. goodFirst controls position; the expected
// answer is "A" or "B" accordingly.
func JudgePrompt(c JudgeCase, goodFirst bool) (prompt, want string) {
	a, b, want := c.Good, c.Bad, "A"
	if !goodFirst {
		a, b, want = c.Bad, c.Good, "B"
	}
	return "Goal: " + c.Goal + "\n\nArtifact A:\n```\n" + a + "\n```\n\nArtifact B:\n```\n" +
		b + "\n```\n\nWhich artifact better meets the goal? Reply with only A or B.", want
}

// ScoreGen reports whether a generator answer satisfies the case.
func ScoreGen(c GenCase, answer string) bool {
	got := normalizeAnswer(answer)
	switch c.Check {
	case CheckJSON:
		if i := strings.Index(got, "{"); i >= 0 {
			if j := strings.LastIndex(got, "}"); j > i {
				got = got[i : j+1]
			}
		}
		var a, b any
		if json.Unmarshal([]byte(got), &a) != nil || json.Unmarshal([]byte(c.Want), &b) != nil {
			return false
		}
		return reflect.DeepEqual(a, b)
	default:
		return got == normalizeAnswer(c.Want)
	}
}

// ScoreJudge reports whether the answer picked the known better artifact.
func ScoreJudge(answer, want string) bool {
	got := strings.ToUpper(strings.TrimSpace(answer))
	if strings.HasPrefix(got, "A") {
		got = "A"
	} else if strings.HasPrefix(got, "B") {
		got = "B"
	} else {
		return false
	}
	return got == want
}

func normalizeAnswer(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`\"'")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	for _, p := range []string{"answer:", "answer =", "the answer is"} {
		if strings.HasPrefix(lower, p) {
			s = strings.TrimSpace(s[len(p):])
			break
		}
	}
	s = strings.Trim(s, "`\"'")
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".")
	return strings.Join(strings.Fields(s), " ")
}

// CaseResult is one completed ladder case.
type CaseResult struct {
	Role    string `json:"role"`
	Model   string `json:"model"`
	CaseID  string `json:"case_id"`
	Correct bool   `json:"correct"`
}

// LadderState is the resumable state file. Results are keyed by
// role|model|case so a rerun skips completed work.
type LadderState struct {
	Version int                   `json:"version"`
	Results map[string]CaseResult `json:"results"`
}

// NewLadderState returns an empty state at the current schema version.
func NewLadderState() LadderState {
	return LadderState{Version: LadderVersion, Results: map[string]CaseResult{}}
}

// StateKey is the stable key for one case.
func StateKey(role, model, caseID string) string {
	return role + "|" + model + "|" + caseID
}

// Done reports whether a case already has a result.
func (s LadderState) Done(role, model, caseID string) bool {
	_, ok := s.Results[StateKey(role, model, caseID)]
	return ok
}

// Record stores a result. A state loaded from an older schema is reset rather
// than mixed with the new instrument.
func (s *LadderState) Record(r CaseResult) {
	if s.Version != LadderVersion || s.Results == nil {
		*s = NewLadderState()
	}
	s.Results[StateKey(r.Role, r.Model, r.CaseID)] = r
}

// Measurement aggregates a model's completed cases for one role. ok is false
// until at least one case exists.
func (s LadderState) Measurement(role, model string) (Measurement, bool) {
	total, correct := 0, 0
	for _, r := range s.Results {
		if r.Role != role || r.Model != model {
			continue
		}
		total++
		if r.Correct {
			correct++
		}
	}
	if total == 0 {
		return Measurement{}, false
	}
	return Measurement{Model: model, Role: role, Score: float64(correct) / float64(total), Samples: total}, true
}

// Measurements returns every (role, model) aggregate, deterministically sorted.
func (s LadderState) Measurements(roles []string, models []string) []Measurement {
	var out []Measurement
	for _, role := range roles {
		for _, model := range models {
			if m, ok := s.Measurement(role, model); ok {
				out = append(out, m)
			}
		}
	}
	return out
}
