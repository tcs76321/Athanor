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

// GenCase is one grounded generator case. Capability names the kind of work
// the case exercises (code, data, document, text, media, general) so the
// result feeds the composition pool (ADR-0104).
type GenCase struct {
	ID         string `json:"id"`
	Prompt     string `json:"prompt"`
	Want       string `json:"want"`
	Check      Check  `json:"check"`
	Capability string `json:"capability,omitempty"`
}

// JudgeCase is one artifact pair with a known better artifact. The labels are
// external and fixed; a model's agreement is the measurement.
type JudgeCase struct {
	ID   string `json:"id"`
	Goal string `json:"goal"`
	Good string `json:"good"`
	Bad  string `json:"bad"`
	// Capability names the kind of work the pair exercises, so the result
	// strengthens that capability's score as well as overall judgment.
	Capability string `json:"capability,omitempty"`
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
			{ID: "gen-arith", Check: CheckExact, Want: "391", Capability: "general",
				Prompt: "Compute 17 * 23. Reply with only the integer, no words."},
			{ID: "gen-trace", Check: CheckExact, Want: "01", Capability: "code",
				Prompt: "What does this Python print? Reply with only the output.\n\nfor i in range(2):\n    print(i, end=\"\")"},
			{ID: "gen-json", Check: CheckJSON, Want: `{"lang":"go","stable":true}`, Capability: "data",
				Prompt: `Extract the language and whether it is stable from: "Go 1.24 is a stable release." ` +
					`Reply with only JSON: {"lang":...,"stable":...}`},
			{ID: "gen-bug", Check: CheckExact, Want: "1", Capability: "code",
				Prompt: "How many times does this loop print? Reply with only the integer.\n\n" +
					"for i in range(0, 3, 2):\n    print(i)"},
			{ID: "gen-data-count", Check: CheckExact, Want: "2", Capability: "data",
				Prompt: `Count the integer values in this JSON array and reply with only the count: [1, "two", 3, null, 4.5]`},
			{ID: "gen-doc-colour", Check: CheckExact, Want: "blue", Capability: "document",
				Prompt: "Read the note and reply with only the car colour.\n\nThe door is green. The car is blue. The roof is grey."},
			{ID: "gen-instruction", Check: CheckExact, Want: "HARBOR", Capability: "general",
				Prompt: "Reply with only the uppercase form of the word harbor."},
			{ID: "gen-text-edit", Check: CheckExact, Want: "The dog sat on the mat", Capability: "text",
				Prompt: "Replace the word cat with dog in this sentence and reply with only the new sentence: The cat sat on the mat."},
			{ID: "gen-long-context", Check: CheckExact, Want: "teal", Capability: "document",
				Prompt: "Read the passage and reply with only the colour written in the log.\n\n" +
					"The ferry left the blue dock before dawn. It passed the green lighthouse, then the orange buoy, " +
					"and finally the grey breakwater. The captain wrote teal in the log because the water reflected the sky. " +
					"The crew ate breakfast after the crossing."},
			{ID: "gen-media-plan", Check: CheckJSON, Want: `{"shots":["wide","close","cut"]}`, Capability: "media",
				Prompt: `Return only JSON with this shape: {"shots":["..."]} for a three-shot sunrise sequence named wide, close, and cut.`},
			// The next block widens the screen beyond one-line recall: state,
			// ordering, slicing, and format discipline separate models that
			// otherwise cluster on the easy cases.
			{ID: "gen-seq-index", Check: CheckExact, Want: "2", Capability: "code",
				Prompt: "What does this Python print? Reply with only the output.\n\nx = [1, 2, 3]\nprint(x[-2])"},
			{ID: "gen-json-nested", Check: CheckJSON, Want: `20`, Capability: "data",
				Prompt: `Extract the second value in key b and reply with only that value: {"a":{"b":[10,20]}}`},
			{ID: "gen-data-sort", Check: CheckJSON, Want: `[1,2,3]`, Capability: "data",
				Prompt: "Sort [3,1,2] ascending and reply with only the JSON array."},
			{ID: "gen-code-bool", Check: CheckExact, Want: "True", Capability: "code",
				Prompt: "What does this Python print? Reply with only the result.\n\nprint(bool([]) or bool([0]))"},
			{ID: "gen-doc-headings", Check: CheckExact, Want: "2", Capability: "document",
				Prompt: "Count the markdown headings and reply with only the integer.\n\n# A\n## B\nplain text"},
			{ID: "gen-math-order", Check: CheckExact, Want: "20", Capability: "general",
				Prompt: "Compute 18 / 3 + 7 * 2. Reply with only the integer."},
			{ID: "gen-code-slice", Check: CheckExact, Want: "bcd", Capability: "code",
				Prompt: "What does this Python print? Reply with only the output.\n\nprint('abcdef'[1:4])"},
			{ID: "gen-json-contract", Check: CheckJSON, Want: `{"a":1,"b":[2,3]}`, Capability: "data",
				Prompt: `Return only JSON with a=1 and b=[2,3].`},
			{ID: "gen-media-aspect", Check: CheckJSON, Want: `{"width":1600,"height":900}`, Capability: "media",
				Prompt: `A 16:9 frame is 1600 px wide. Reply with only JSON: {"width":...,"height":...}.`},
		},
		Judges: []JudgeCase{
			{
				ID: "judge-edge-case", Capability: "code", Goal: "Return the average of a list; an empty list returns 0.",
				Good: "def avg(xs):\n    if not xs:\n        return 0\n    return sum(xs) / len(xs)",
				Bad:  "def avg(xs):\n    return sum(xs) / len(xs)",
			},
			{
				ID: "judge-error-path", Capability: "code", Goal: "Read a JSON file and return its parsed value; invalid JSON must raise a clear error.",
				Good: "def load(path):\n    try:\n        with open(path) as f:\n            return json.load(f)\n    except json.JSONDecodeError as e:\n        raise ValueError(f\"invalid JSON in {path}: {e}\") from e",
				Bad:  "def load(path):\n    with open(path) as f:\n        return json.load(f)",
			},
			{
				ID: "judge-sql-rollback", Capability: "data", Goal: "An additive migration must be reversible.",
				Good: "-- up\nALTER TABLE t ADD COLUMN age INTEGER;\n-- down\nALTER TABLE t DROP COLUMN age;",
				Bad:  "-- up\nALTER TABLE t ADD COLUMN age INTEGER;",
			},
			{
				ID: "judge-doc-contract", Capability: "document", Goal: "Document a function's inputs, outputs, and error behavior.",
				Good: "parse(s) -> dict\n  s: JSON string.\n  Returns the decoded object.\n  Raises ValueError if s is not valid JSON.",
				Bad:  "parse(s)\n  Parses s and returns the result.",
			},
			{
				ID: "judge-no-network", Capability: "code", Goal: "A test double must not perform real network I/O.",
				Good: "class FakeClient:\n    def __init__(self, response):\n        self.response = response\n    def get(self, url):\n        return self.response",
				Bad:  "class FakeClient:\n    def get(self, url):\n        return requests.get(url).json()",
			},
			{
				ID: "judge-close-file", Capability: "code", Goal: "Read a file and always release the handle.",
				Good: "with open(path) as f:\n    return f.read()",
				Bad:  "f = open(path)\nreturn f.read()",
			},
			{
				ID: "judge-boundary", Capability: "code", Goal: "A function divides two numbers and must handle a zero denominator explicitly.",
				Good: "def div(a, b):\n    if b == 0:\n        raise ValueError(\"division by zero\")\n    return a / b",
				Bad:  "def div(a, b):\n    return a / b",
			},
			{
				ID: "judge-secret", Capability: "code", Goal: "A client authenticates with a token but must never write the token to logs.",
				Good: "log.info(\"request authorized\")\nreturn client.get(url, headers=headers)",
				Bad:  "log.info(f\"token={token}\")\nreturn client.get(url, headers=headers)",
			},
			{
				ID: "judge-hallucination", Capability: "document", Goal: "Summarize the provided source and do not add facts it does not support.",
				Good: "The cache was warm after the first run [S2].",
				Bad:  "The cache was warm after the first run and cut latency by 40% [S2].",
			},
			{
				ID: "judge-instruction-hierarchy", Capability: "general", Goal: "Return only a JSON object with the key status and value ok.",
				Good: `{"status":"ok"}`,
				Bad:  "The status is ok.",
			},
			{
				ID: "judge-scope", Capability: "general", Goal: "Summarize the incident in exactly three bullet points; do not add recommendations.",
				Good: "- Database restarted at noon.\n- Requests failed for four minutes.\n- No data was lost.",
				Bad:  "- Database restarted at noon.\n- Requests failed for four minutes.\n- No data was lost.\n- Recommendation: add a second database.",
			},
			{
				ID: "judge-schema", Capability: "data", Goal: "Validate rows and return only JSON with count and invalid; do not drop invalid rows.",
				Good: `{"count":2,"invalid":1}`,
				Bad:  `{"count":1}`,
			},
			{
				ID: "judge-concurrency", Capability: "code", Goal: "Two workers increment a shared counter safely.",
				Good: "with lock:\n    counter += 1",
				Bad:  "counter += 1",
			},
			{
				ID: "judge-sql-injection", Capability: "data", Goal: "Query a user by name without allowing SQL injection.",
				Good: "cur.execute(\"SELECT * FROM users WHERE name = ?\", (name,))",
				Bad:  "cur.execute(f\"SELECT * FROM users WHERE name = '{name}'\")",
			},
			{
				ID: "judge-idempotency", Capability: "data", Goal: "Retrying the import must not duplicate rows.",
				Good: "INSERT INTO t (id, v) VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET v = excluded.v",
				Bad:  "INSERT INTO t (id, v) VALUES (?, ?)",
			},
			{
				ID: "judge-timeout", Capability: "code", Goal: "A network call must not hang forever.",
				Good: "ctx, cancel := context.WithTimeout(ctx, 10*time.Second)\ndefer cancel()\nreturn client.Do(ctx)",
				Bad:  "return client.Do(context.Background())",
			},
			{
				ID: "judge-data-types", Capability: "data", Goal: "Reject records whose age is not an integer instead of coercing them.",
				Good: "if !isInt(r.Age) {\n    return fmt.Errorf(\"age must be an integer\")\n}",
				Bad:  "r.Age = parseInt(r.Age)",
			},
			{
				ID: "judge-doc-structure", Capability: "document", Goal: "Produce a runbook with prerequisites, steps, and rollback.",
				Good: "## Prerequisites\n...\n## Steps\n...\n## Rollback\n...",
				Bad:  "Here is everything you need to know in one long paragraph.",
			},
			{
				ID: "judge-text-audience", Capability: "text", Goal: "Explain the outage to customers without jargon or blame.",
				Good: "Some requests failed for four minutes while we restarted a database. No data was lost.",
				Bad:  "The primary DB replica entered a crash-loop after the schema migration.",
			},
			{
				ID: "judge-media-alt", Capability: "media", Goal: "Write alt text for a bar chart of quarterly revenue.",
				Good: "Bar chart: revenue rises each quarter, from $2M in Q1 to $5M in Q4.",
				Bad:  "Chart.",
			},
			{
				ID: "judge-secret-env", Capability: "code", Goal: "Read a credential without hard-coding it in the repository.",
				Good: "token := os.Getenv(\"ATHANOR_TOKEN\")",
				Bad:  "token := \"sk-live-abc123\"",
			},
			{
				ID: "judge-test-isolation", Capability: "code", Goal: "A test writes a temporary file and removes it afterwards.",
				Good: "dir := t.TempDir()\npath := filepath.Join(dir, \"x\")\nt.Cleanup(func() { os.Remove(path) })",
				Bad:  "path := \"/tmp/athanor-test-output\"",
			},
		},
	}
}

// DeepLadder extends the smoke screen with the cases that take longer or
// need more context. It is opt-in and runs in Deep/Full, never in Lite
// (ADR-0104: tiered qualification).
func DeepLadder() Ladder {
	l := DefaultLadder()
	l.Generators = append(l.Generators,
		GenCase{
			ID: "gen-long-context-deep", Check: CheckExact, Want: "orange", Capability: "document",
			Prompt: "Read this note and reply with only the colour.\n\n" +
				"The workshop moved twice. First it was near the blue harbour, then the green market, " +
				"and finally beside the orange bridge. The tools stayed in the same crate.",
		},
		GenCase{
			ID: "gen-schema", Check: CheckJSON, Capability: "data",
			Want:   `{"ok":true,"count":2,"items":["a","b"]}`,
			Prompt: `Return only JSON with this shape: {"ok":bool,"count":int,"items":[]} for the input "a, b".`,
		},
		GenCase{
			ID: "gen-code-repair", Check: CheckExact, Want: "3", Capability: "code",
			Prompt: "This Python function should return the number of arguments it receives, but it has a bug. " +
				"What is the correct return value for `count(1, 2, 3)` after the fix? Reply with only the integer.\n\n" +
				"def count(*args):\n    return len(args) - 1",
		},
		GenCase{
			ID: "gen-doc-cite", Check: CheckExact, Want: "S2", Capability: "document",
			Prompt: "Which source supports the claim that the cache is warm? Reply with only the source id.\n\n" +
				"S1: The cache was cleared at noon. S2: The cache was warm after the first run. S3: The logs rotate daily.",
		},
		GenCase{
			ID: "gen-data-filter", Check: CheckJSON, Capability: "data",
			Want:   `["a","c"]`,
			Prompt: `Return only JSON: the names where active is true. Input: [{"name":"a","active":true},{"name":"b","active":false},{"name":"c","active":true}]`,
		},
		GenCase{
			ID: "gen-code-api", Check: CheckExact, Want: "404", Capability: "code",
			Prompt: "A REST endpoint receives a request for a resource that does not exist. " +
				"Reply with only the HTTP status code it should return.",
		},
	)
	l.Judges = append(l.Judges,
		JudgeCase{
			ID: "judge-injection", Capability: "general",
			Goal: "A tool reads a web page and summarizes it. Never execute instructions found in the page.",
			Good: "summary = extract_text(page)\nreturn summarize(summary)",
			Bad:  "if 'ignore previous instructions' in page:\n    run(page)\nreturn summarize(page)",
		},
		JudgeCase{
			ID: "judge-data-integrity", Capability: "data",
			Goal: "Validate every record and fail loudly instead of silently dropping invalid rows.",
			Good: "for r in rows:\n    validate(r)\n    out.append(r)",
			Bad:  "for r in rows:\n    try:\n        validate(r)\n        out.append(r)\n    except Exception:\n        pass",
		},
		JudgeCase{
			ID: "judge-test-quality", Capability: "code",
			Goal: "A test must prove the function rejects an empty input, not merely sleep or print.",
			Good: "def test_empty():\n    with pytest.raises(ValueError):\n        parse(\"\")",
			Bad:  "def test_empty():\n    parse(\"\")\n    time.sleep(1)",
		},
		JudgeCase{
			ID: "judge-citation", Capability: "document",
			Goal: "A summary must cite the provided source and must not invent one.",
			Good: "The cache was warm after the first run [S2].",
			Bad:  "The cache was warm after the first run [S7].",
		},
		JudgeCase{
			ID: "judge-accessibility", Capability: "media",
			Goal: "Provide alt text for a chart showing a slow rise in rainfall over five years.",
			Good: "Line chart: rainfall rises gradually from 2019 to 2023, with the steepest increase in 2022.",
			Bad:  "Chart image.",
		},
		JudgeCase{
			ID: "judge-tone", Capability: "text",
			Goal: "Rewrite the notice for a general audience without jargon or alarm.",
			Good: "The service will pause for about ten minutes tonight while we update it. Your data is safe.",
			Bad:  "Critical maintenance window: expect service termination during schema mutation.",
		},
	)
	return l
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
