// Package planning turns a chosen discovery direction into a small set of
// AI-authored, reviewable plans. It is deliberately separate from the
// deterministic plan builder: the model proposes, the validator and scorer
// constrain, and the human selects before the DAG is created.
package planning

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// Task is one plan task before it becomes a project.TaskSpec.
type Task struct {
	Key         string   `json:"key,omitempty"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Capability  string   `json:"capability"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Criteria    []string `json:"criteria"`
}

// Stances are the three distinct plan shapes. The seed call returns one of
// each; the expansion calls preserve the stance.
const (
	StanceSmallest   = "smallest"
	StanceDeepest    = "deepest"
	StanceLowestRisk = "lowest_risk"
)

// Seed is the compact result of the first planning call. It is a scaffold for
// one expansion call, not an approvable plan.
type Seed struct {
	Stance      string   `json:"stance"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	TaskTitles  []string `json:"task_titles"`
	Risks       []string `json:"risks,omitempty"`
	Assumptions []string `json:"assumptions,omitempty"`
}

// Candidate is one complete plan proposal. The three candidates differ by
// stance, not by a synthetic score or the model's self-reported confidence.
type Candidate struct {
	Stance      string   `json:"stance,omitempty"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	Tasks       []Task   `json:"tasks"`
	Risks       []string `json:"risks"`
	Assumptions []string `json:"assumptions"`
	// Status and Error are persisted run metadata, not model output.
	Status string `json:"-"`
	Error  string `json:"-"`
}

// Session is one durable planning run for a project.
type Session struct {
	ID            string
	ProjectID     string
	OptionID      string
	Status        string
	Error         string
	StopReason    string
	Stage         string
	Model         string
	PromptBytes   int
	ResponseBytes int
	Tail          string
	TotalTokens   int
	ExpandedCount int
	HeartbeatAt   time.Time
	SelectedIndex int
	Candidates    []Candidate
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ProgressEvent is one observable step in a planning run.
type ProgressEvent struct {
	Phase         string
	Stage         string
	Detail        string
	Model         string
	ContextTarget int
	PromptBytes   int
	ResponseBytes int
	Tail          string
	Candidates    int
	PlanIndex     int
	Tokens        int
}

// Report emits one progress event when an observer is attached.
func (r Request) Report(ev ProgressEvent) {
	if r.Progress != nil {
		r.Progress(ev)
	}
}

// Request is the generator input.
type Request struct {
	Goal        string
	Brief       string
	Constraints []string
	Sources     []string
	OptionTitle string
	Problem     string
	Scope       string
	Approach    string
	Deliverable string
	Check       string
	// Phase is "seed" for the three-plan scaffold call, "expand" for one
	// plan's expansion, or empty for the legacy single-call contract.
	Phase string
	// Seed and OtherSeeds are set for an expansion call.
	Seed       *Seed
	OtherSeeds []Seed
	// Model and ContextTarget pin every call in a run to the model that served
	// the seed call, so the three plans are comparable.
	Model         string
	ContextTarget int
	Progress      func(ProgressEvent)
	// Repair carries a bounded rejection reason on a retry after a parse or
	// validation failure.
	Repair string
}

// Generator produces the raw JSON plan array.
type Generator interface {
	Generate(ctx context.Context, req Request) (string, error)
}

// BuildPrompt is the closed plan-generation contract.
func BuildPrompt(req Request) string {
	switch req.Phase {
	case "seed":
		return buildSeedPrompt(req)
	case "expand":
		return buildExpandPrompt(req)
	}
	var b strings.Builder
	b.WriteString("You are Athanor's planning agent. Produce exactly 3 distinct plans for the chosen direction below. A plan is a short, ordered first iteration, not a fixed-length schedule.\n\n")
	fmt.Fprintf(&b, "GOAL: %s\n\n", strings.TrimSpace(req.Goal))
	if req.Brief != "" {
		fmt.Fprintf(&b, "BRIEF:\n%s\n\n", strings.TrimSpace(req.Brief))
	}
	fmt.Fprintf(&b, "CHOSEN DIRECTION: %s\n", req.OptionTitle)
	if req.Problem != "" {
		fmt.Fprintf(&b, "PROBLEM: %s\n", req.Problem)
	}
	if req.Scope != "" {
		fmt.Fprintf(&b, "SCOPE: %s\n", req.Scope)
	}
	if req.Approach != "" {
		fmt.Fprintf(&b, "APPROACH: %s\n", req.Approach)
	}
	if req.Deliverable != "" {
		fmt.Fprintf(&b, "FIRST DELIVERABLE: %s\n", req.Deliverable)
	}
	if req.Check != "" {
		fmt.Fprintf(&b, "CHECK: %s\n", req.Check)
	}
	if len(req.Constraints) > 0 {
		b.WriteString("\nCONSTRAINTS:\n")
		for _, c := range req.Constraints {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	if len(req.Sources) > 0 {
		b.WriteString("\nALLOWED SOURCES:\n")
		for _, s := range req.Sources {
			fmt.Fprintf(&b, "- %s\n", s)
		}
	}
	b.WriteString("\nReturn ONLY a JSON array of exactly 3 objects. Each object: {title, summary, tasks:[{title, description, capability, criteria:[...]}], risks:[...], assumptions:[...]}.\n")
	b.WriteString("RULES:\n")
	b.WriteString("- The plans must be genuinely different: one smallest verifiable slice, one deepest coverage, one lowest-risk sequence. Name the stance in the title or summary.\n")
	b.WriteString("- Tasks must be concrete and ordered; include 4–8 tasks per plan.\n")
	b.WriteString("- capability must be one of: code, data, document, text, media.\n")
	b.WriteString("- Every task must be small enough to finish in one job and have at least one acceptance criterion.\n")
	b.WriteString("- Do not estimate time, cost, human effort, or required skills; Athanor runs locally under its own budget.\n")
	b.WriteString("- Cite only the allowed sources; do not invent data or citations.\n")
	b.WriteString("- Output only the JSON array, no markdown or commentary.\n")
	if repair := strings.TrimSpace(req.Repair); repair != "" {
		if len(repair) > 500 {
			repair = repair[:500] + "…"
		}
		b.WriteString("\nREPAIR REQUIRED: the previous response was rejected: " + repair + "\n")
		b.WriteString("Return ONE JSON array of exactly three complete plan objects. Every object must include title, summary, tasks with title/description/capability/criteria, risks, and assumptions. Do not return two arrays, concatenated objects, or a partial object.\n")
	}
	return b.String()
}

// buildSeedPrompt is the first of four calls: three compact plan seeds, one
// per stance. The seeds are scaffolding; expansion calls produce the full
// plans.
func buildSeedPrompt(req Request) string {
	var b strings.Builder
	b.WriteString("You are Athanor's planning scout. Produce exactly three distinct plan seeds for the chosen direction below. A seed is a compact scaffold, not a complete plan.\n\n")
	writePlanningContext(&b, req)
	b.WriteString("\nReturn ONLY a JSON array of exactly 3 objects. Each object: {stance, title, summary, task_titles:[...], risks:[...], assumptions:[...]}.\n")
	b.WriteString("RULES:\n")
	b.WriteString("- stance must be exactly one of: smallest, deepest, lowest_risk. Use each once.\n")
	b.WriteString("- smallest is the smallest verifiable slice; deepest is the deepest useful coverage; lowest_risk is the sequence that retires the most uncertainty first.\n")
	b.WriteString("- Give each seed 3–6 concrete task titles. Do not write task descriptions or criteria here.\n")
	b.WriteString("- The seeds must be genuinely different; do not vary only the wording.\n")
	writePlanningRules(&b, req)
	return b.String()
}

// buildExpandPrompt is one expansion call: turn exactly one seed into one
// complete plan, preserving its stance and differentiating it from the other
// seeds. This is the single polish pass; a repair retry only fixes malformed
// output.
func buildExpandPrompt(req Request) string {
	var b strings.Builder
	b.WriteString("You are Athanor's planning agent. Expand exactly ONE seed into one complete, executable plan. This is the single expansion/polish pass for this plan; do not return other plans.\n\n")
	writePlanningContext(&b, req)
	if req.Seed != nil {
		fmt.Fprintf(&b, "\nSEED TO EXPAND:\n- stance: %s\n- title: %s\n- summary: %s\n- task titles: %s\n", req.Seed.Stance, req.Seed.Title, req.Seed.Summary, strings.Join(req.Seed.TaskTitles, " | "))
	}
	if len(req.OtherSeeds) > 0 {
		b.WriteString("\nOTHER SEEDS (do not merge into this plan; make this one clearly different):\n")
		for _, s := range req.OtherSeeds {
			fmt.Fprintf(&b, "- %s [%s]: %s\n", s.Title, s.Stance, s.Summary)
		}
	}
	b.WriteString("\nReturn ONLY one JSON object: {stance, title, summary, tasks:[{key, title, description, capability, depends_on:[...], criteria:[...]}], risks:[...], assumptions:[...]}.\n")
	b.WriteString("RULES:\n")
	b.WriteString("- Preserve the seed's stance exactly. Do not turn it into a different stance.\n")
	b.WriteString("- Include 4–8 tasks. Each task needs a unique key, a concrete description, a capability, and at least one acceptance criterion.\n")
	b.WriteString("- depends_on lists task keys; the graph must be acyclic. A task with no dependencies may start in parallel.\n")
	b.WriteString("- capability must be one of: code, data, document, text, media.\n")
	b.WriteString("- Do not estimate time, cost, human effort, or required skills; Athanor runs locally under its own budget.\n")
	b.WriteString("- Cite only the allowed sources; do not invent data or citations.\n")
	b.WriteString("- Output only the JSON object, with no markdown or commentary.\n")
	if repair := strings.TrimSpace(req.Repair); repair != "" {
		if len(repair) > 500 {
			repair = repair[:500] + "…"
		}
		b.WriteString("\nREPAIR REQUIRED: the previous response was rejected: " + repair + "\n")
		b.WriteString("Return ONE complete JSON object with stance, title, summary, 4–8 tasks (key/title/description/capability/depends_on/criteria), risks, and assumptions. Do not return an array of plans or a partial object.\n")
	}
	return b.String()
}

func writePlanningContext(b *strings.Builder, req Request) {
	fmt.Fprintf(b, "GOAL: %s\n", strings.TrimSpace(req.Goal))
	if req.Brief != "" {
		fmt.Fprintf(b, "BRIEF:\n%s\n", strings.TrimSpace(req.Brief))
	}
	fmt.Fprintf(b, "CHOSEN DIRECTION: %s\n", req.OptionTitle)
	if req.Problem != "" {
		fmt.Fprintf(b, "PROBLEM: %s\n", req.Problem)
	}
	if req.Scope != "" {
		fmt.Fprintf(b, "SCOPE: %s\n", req.Scope)
	}
	if req.Approach != "" {
		fmt.Fprintf(b, "APPROACH: %s\n", req.Approach)
	}
	if req.Deliverable != "" {
		fmt.Fprintf(b, "FIRST DELIVERABLE: %s\n", req.Deliverable)
	}
	if req.Check != "" {
		fmt.Fprintf(b, "CHECK: %s\n", req.Check)
	}
	if len(req.Constraints) > 0 {
		b.WriteString("CONSTRAINTS:\n")
		for _, c := range req.Constraints {
			fmt.Fprintf(b, "- %s\n", c)
		}
	}
	if len(req.Sources) > 0 {
		b.WriteString("ALLOWED SOURCES:\n")
		for _, s := range req.Sources {
			fmt.Fprintf(b, "- %s\n", s)
		}
	}
}

func writePlanningRules(b *strings.Builder, req Request) {
	b.WriteString("- Do not estimate time, cost, human effort, or required skills; Athanor runs locally under its own budget.\n")
	b.WriteString("- Cite only the allowed sources; do not invent data or citations.\n")
	b.WriteString("- Output only the requested JSON, no markdown or commentary.\n")
	if repair := strings.TrimSpace(req.Repair); repair != "" {
		if len(repair) > 500 {
			repair = repair[:500] + "…"
		}
		b.WriteString("\nREPAIR REQUIRED: the previous response was rejected: " + repair + "\n")
		b.WriteString("Return exactly three complete seed objects, one per stance.\n")
	}
}

// Parse extracts plan candidates from a model response. Models sometimes emit
// more than one top-level value — two arrays, concatenated objects, or a
// wrapped object followed by another array — which a single json.Unmarshal
// rejects with "invalid character ',' after top-level value". The parser
// scans complete top-level JSON values, parses each one, and merges the
// candidates. A truncated final array is salvaged for its complete objects.
func Parse(raw string) ([]Candidate, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	var out []Candidate
	for _, value := range topLevelValues(s) {
		out = append(out, parsePlanValue(value)...)
	}
	if len(out) == 0 {
		// Compatibility fallback for a response whose outer syntax is too
		// damaged for the scanner: keep the old bounded extraction, but only
		// as a last resort.
		if start := strings.Index(s, "["); start >= 0 {
			if end := strings.LastIndex(s, "]"); end > start {
				var arr []Candidate
				if err := json.Unmarshal([]byte(s[start:end+1]), &arr); err == nil && len(arr) > 0 {
					out = append(out, arr...)
				}
			}
			if len(out) == 0 {
				out = append(out, parseSequence(s[start:])...)
			}
		}
		if len(out) == 0 {
			if start := strings.Index(s, "{"); start >= 0 {
				out = append(out, parsePlanValue(s[start:])...)
			}
		}
	}
	out = Dedupe(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("planning: response contains no complete plan objects")
	}
	return out, nil
}

// topLevelValues scans complete top-level JSON arrays/objects. It skips prose,
// commas, and separators between values; a truncated final value is returned
// as the remainder so the caller can salvage its complete objects.
func topLevelValues(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		for i < len(s) {
			switch s[i] {
			case ' ', '\t', '\n', '\r', ',', ';':
				i++
				continue
			}
			break
		}
		if i >= len(s) {
			break
		}
		if s[i] != '[' && s[i] != '{' {
			// Prose: advance to the next plausible JSON value.
			for i < len(s) && s[i] != '[' && s[i] != '{' {
				i++
			}
			continue
		}
		start := i
		depth := 0
		inString := false
		escaped := false
		closed := false
		for ; i < len(s); i++ {
			c := s[i]
			if inString {
				if escaped {
					escaped = false
					continue
				}
				if c == '\\' {
					escaped = true
					continue
				}
				if c == '"' {
					inString = false
				}
				continue
			}
			switch c {
			case '"':
				inString = true
			case '[', '{':
				depth++
			case ']', '}':
				depth--
				if depth == 0 {
					i++
					closed = true
				}
			}
			if closed {
				break
			}
		}
		if closed {
			out = append(out, s[start:i])
			continue
		}
		// A truncated tail: keep it for salvage, then stop.
		out = append(out, s[start:])
		break
	}
	return out
}

// parsePlanValue parses one top-level value as an array of candidates, a
// wrapped {plans|[...]} object, or a single candidate object.
func parsePlanValue(raw string) []Candidate {
	t := strings.TrimSpace(raw)
	if t == "" {
		return nil
	}
	switch t[0] {
	case '[':
		var arr []Candidate
		if err := json.Unmarshal([]byte(t), &arr); err == nil {
			return arr
		}
		// A truncated or trailing-comma array: scan its complete object
		// values directly rather than relying on one Decode pass, which stops
		// at the comma before the next object.
		inner := strings.TrimSpace(strings.TrimPrefix(t, "["))
		inner = strings.TrimSuffix(inner, "]")
		var out []Candidate
		for _, value := range topLevelValues(inner) {
			out = append(out, parsePlanValue(value)...)
		}
		if len(out) > 0 {
			return out
		}
		return parseSequence(inner)
	case '{':
		var wrapped struct {
			Plans      []Candidate `json:"plans"`
			Candidates []Candidate `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(t), &wrapped); err == nil {
			if len(wrapped.Plans) > 0 {
				return wrapped.Plans
			}
			if len(wrapped.Candidates) > 0 {
				return wrapped.Candidates
			}
		}
		var c Candidate
		if err := json.Unmarshal([]byte(t), &c); err == nil && strings.TrimSpace(c.Title) != "" {
			return []Candidate{c}
		}
	}
	return nil
}

// ParseSeeds extracts the three seed objects from a response. It accepts an
// array, concatenated objects, a {"seeds":[...]} wrapper, or a truncated tail.
func ParseSeeds(raw string) ([]Seed, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	var out []Seed
	for _, value := range topLevelValues(s) {
		out = append(out, parseSeedValue(value)...)
	}
	if len(out) == 0 {
		if start := strings.Index(s, "["); start >= 0 {
			var arr []Seed
			if err := json.Unmarshal([]byte(s[start:]), &arr); err == nil {
				out = append(out, arr...)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("planning: response contains no complete plan seeds")
	}
	return out, nil
}

func parseSeedValue(raw string) []Seed {
	t := strings.TrimSpace(raw)
	if t == "" {
		return nil
	}
	switch t[0] {
	case '[':
		var arr []Seed
		if err := json.Unmarshal([]byte(t), &arr); err == nil {
			return arr
		}
		inner := strings.TrimSpace(strings.TrimPrefix(t, "["))
		inner = strings.TrimSuffix(inner, "]")
		var out []Seed
		for _, value := range topLevelValues(inner) {
			out = append(out, parseSeedValue(value)...)
		}
		return out
	case '{':
		var wrapped struct {
			Seeds []Seed `json:"seeds"`
		}
		if err := json.Unmarshal([]byte(t), &wrapped); err == nil && len(wrapped.Seeds) > 0 {
			return wrapped.Seeds
		}
		var seed Seed
		if err := json.Unmarshal([]byte(t), &seed); err == nil && strings.TrimSpace(seed.Title) != "" {
			return []Seed{seed}
		}
	}
	return nil
}

// ValidateSeeds enforces the three-stance scaffold: exactly three distinct
// stances, each with a title, summary, and 3–6 task titles.
func ValidateSeeds(seeds []Seed) ([]Seed, error) {
	if len(seeds) != 3 {
		return nil, fmt.Errorf("planning: need exactly 3 seeds, got %d", len(seeds))
	}
	seen := map[string]bool{}
	out := make([]Seed, 0, 3)
	for _, s := range seeds {
		s.Stance = strings.ToLower(strings.TrimSpace(s.Stance))
		switch s.Stance {
		case StanceSmallest, StanceDeepest, StanceLowestRisk:
		default:
			return nil, fmt.Errorf("planning: unknown stance %q", s.Stance)
		}
		if seen[s.Stance] {
			return nil, fmt.Errorf("planning: duplicate stance %q", s.Stance)
		}
		seen[s.Stance] = true
		if strings.TrimSpace(s.Title) == "" || strings.TrimSpace(s.Summary) == "" {
			return nil, fmt.Errorf("planning: seed %q needs a title and summary", s.Stance)
		}
		if len(s.TaskTitles) < 3 || len(s.TaskTitles) > 6 {
			return nil, fmt.Errorf("planning: seed %q has %d task titles (want 3–6)", s.Stance, len(s.TaskTitles))
		}
		out = append(out, s)
	}
	return out, nil
}

// ValidateExpanded validates one expanded plan and its task graph. It also
// normalizes missing task keys into stable keys so a model that omits a key
// can still produce a valid acyclic graph.
func ValidateExpanded(c Candidate, seed Seed) (Candidate, error) {
	if strings.TrimSpace(c.Title) == "" || strings.TrimSpace(c.Summary) == "" {
		return Candidate{}, fmt.Errorf("planning: expanded plan needs a title and summary")
	}
	c.Stance = strings.ToLower(strings.TrimSpace(c.Stance))
	if c.Stance == "" {
		c.Stance = seed.Stance
	}
	if c.Stance != seed.Stance {
		return Candidate{}, fmt.Errorf("planning: expanded plan changed stance from %q to %q", seed.Stance, c.Stance)
	}
	if len(c.Tasks) < 4 || len(c.Tasks) > 8 {
		return Candidate{}, fmt.Errorf("planning: expanded plan has %d tasks (want 4–8)", len(c.Tasks))
	}
	keys := map[string]bool{}
	for i := range c.Tasks {
		t := &c.Tasks[i]
		t.Key = strings.TrimSpace(t.Key)
		if t.Key == "" {
			t.Key = fmt.Sprintf("task-%d", i+1)
		}
		if keys[t.Key] {
			return Candidate{}, fmt.Errorf("planning: duplicate task key %q", t.Key)
		}
		keys[t.Key] = true
		if strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Description) == "" {
			return Candidate{}, fmt.Errorf("planning: task %q has an empty title or description", t.Key)
		}
		if !project.ValidCapability(t.Capability) {
			return Candidate{}, fmt.Errorf("planning: task %q has invalid capability %q", t.Key, t.Capability)
		}
		if len(t.Criteria) == 0 {
			return Candidate{}, fmt.Errorf("planning: task %q has no acceptance criterion", t.Key)
		}
	}
	for i := range c.Tasks {
		for _, dep := range c.Tasks[i].DependsOn {
			if !keys[dep] {
				return Candidate{}, fmt.Errorf("planning: task %q depends on unknown key %q", c.Tasks[i].Key, dep)
			}
			if dep == c.Tasks[i].Key {
				return Candidate{}, fmt.Errorf("planning: task %q depends on itself", dep)
			}
		}
	}
	if err := validateAcyclic(c.Tasks); err != nil {
		return Candidate{}, err
	}
	if c.Risks == nil {
		c.Risks = []string{}
	}
	if c.Assumptions == nil {
		c.Assumptions = []string{}
	}
	return c, nil
}

func validateAcyclic(tasks []Task) error {
	state := map[string]int{} // 0 unvisited, 1 visiting, 2 done
	byKey := map[string]Task{}
	for _, t := range tasks {
		byKey[t.Key] = t
	}
	var visit func(string) error
	visit = func(key string) error {
		switch state[key] {
		case 1:
			return fmt.Errorf("planning: task graph contains a cycle at %q", key)
		case 2:
			return nil
		}
		state[key] = 1
		for _, dep := range byKey[key].DependsOn {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[key] = 2
		return nil
	}
	for _, t := range tasks {
		if err := visit(t.Key); err != nil {
			return err
		}
	}
	return nil
}

// ParseOne extracts exactly one expanded plan object/array element.
func ParseOne(raw string) (Candidate, error) {
	candidates, err := Parse(raw)
	if err != nil {
		return Candidate{}, err
	}
	if len(candidates) != 1 {
		return Candidate{}, fmt.Errorf("planning: expected exactly one expanded plan, got %d", len(candidates))
	}
	return candidates[0], nil
}

// Dedupe removes duplicate plan titles, keeping the first occurrence.
func Dedupe(candidates []Candidate) []Candidate {
	seen := map[string]bool{}
	out := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		key := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(c.Title)), " "))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

// parseSequence decodes concatenated candidate objects from a fragment. It is
// used for a truncated array and for a model that emits objects without the
// enclosing brackets.
func parseSequence(s string) []Candidate {
	dec := json.NewDecoder(strings.NewReader(s))
	var out []Candidate
	for {
		var c Candidate
		if err := dec.Decode(&c); err != nil {
			break
		}
		if strings.TrimSpace(c.Title) != "" {
			out = append(out, c)
		}
	}
	return out
}

// Validate enforces the plan's structural contract and capability set.
func Validate(candidates []Candidate) ([]Candidate, error) {
	if len(candidates) < 3 {
		return nil, fmt.Errorf("planning: need 3 candidates, got %d", len(candidates))
	}
	out := make([]Candidate, 0, 3)
	seenTitles := map[string]bool{}
	for _, c := range candidates {
		title := strings.TrimSpace(c.Title)
		if title == "" {
			return nil, fmt.Errorf("planning: a candidate has no title")
		}
		key := strings.ToLower(title)
		if seenTitles[key] {
			continue
		}
		seenTitles[key] = true
		if strings.TrimSpace(c.Summary) == "" {
			return nil, fmt.Errorf("planning: candidate %q has no summary", title)
		}
		if c.Risks == nil {
			c.Risks = []string{}
		}
		if c.Assumptions == nil {
			c.Assumptions = []string{}
		}
		if len(c.Tasks) < 2 || len(c.Tasks) > 10 {
			return nil, fmt.Errorf("planning: candidate %q has %d tasks (want 2–10)", title, len(c.Tasks))
		}
		taskTitles := map[string]bool{}
		for _, t := range c.Tasks {
			if strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Description) == "" {
				return nil, fmt.Errorf("planning: candidate %q has an empty task", title)
			}
			if !project.ValidCapability(t.Capability) {
				return nil, fmt.Errorf("planning: candidate %q task %q has invalid capability %q", title, t.Title, t.Capability)
			}
			key := strings.ToLower(strings.TrimSpace(t.Title))
			if taskTitles[key] {
				return nil, fmt.Errorf("planning: candidate %q repeats task %q", title, t.Title)
			}
			taskTitles[key] = true
			if len(t.Criteria) == 0 {
				return nil, fmt.Errorf("planning: candidate %q task %q has no acceptance criterion", title, t.Title)
			}
		}
		out = append(out, c)
		if len(out) == 3 {
			break
		}
	}
	if len(out) < 3 {
		return nil, fmt.Errorf("planning: need 3 distinct titled candidates, got %d", len(out))
	}
	return out, nil
}

// Store persists planning sessions.
type Store struct{ db *store.Store }

func NewStore(s *store.Store) *Store { return &Store{db: s} }

// Begin starts a durable planning session for one project and direction. The
// brief itself lives in project_briefs; the session records the option so a
// stale plan cannot be shown after the operator chooses another direction.
func (s *Store) Begin(ctx context.Context, projectID, optionID string) (Session, error) {
	id := ids.New()
	if _, err := s.db.DB().ExecContext(ctx,
		`INSERT INTO plan_sessions (id, project_id, option_id, status, stage, heartbeat_at)
		 VALUES (?, ?, ?, 'generating', 'seed', strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		id, projectID, optionID); err != nil {
		return Session{}, fmt.Errorf("creating plan session: %w", err)
	}
	sess, ok, err := s.getByID(ctx, id)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, fmt.Errorf("planning: session disappeared after begin")
	}
	return sess, nil
}

func (s *Store) Complete(ctx context.Context, sessionID string, candidates []Candidate) error {
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i, c := range candidates {
		tasks, _ := json.Marshal(c.Tasks)
		risks, _ := json.Marshal(c.Risks)
		assumptions, _ := json.Marshal(c.Assumptions)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO plan_candidates (id, session_id, sort_order, stance, status, title, summary, tasks_json, risks_json, assumptions_json)
			VALUES (?, ?, ?, ?, 'ready', ?, ?, ?, ?, ?)`,
			ids.New(), sessionID, i, c.Stance, c.Title, c.Summary, string(tasks), string(risks), string(assumptions)); err != nil {
			return fmt.Errorf("saving plan candidate: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE plan_sessions SET status = 'ready', error = '', stage = 'done',
		 expanded_count = ?, heartbeat_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
		len(candidates), sessionID); err != nil {
		return fmt.Errorf("marking plan ready: %w", err)
	}
	return tx.Commit()
}

// SaveSeeds persists the three seed scaffolds. The candidates are not
// approvable until their expansion calls fill in full tasks.
func (s *Store) SaveSeeds(ctx context.Context, sessionID string, seeds []Seed) error {
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM plan_candidates WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("clearing plan candidates: %w", err)
	}
	for i, seed := range seeds {
		tasks := make([]Task, 0, len(seed.TaskTitles))
		for _, title := range seed.TaskTitles {
			tasks = append(tasks, Task{Title: title})
		}
		tasksJSON, _ := json.Marshal(tasks)
		risksJSON, _ := json.Marshal(seed.Risks)
		assumptionsJSON, _ := json.Marshal(seed.Assumptions)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO plan_candidates (id, session_id, sort_order, stance, status, title, summary, tasks_json, risks_json, assumptions_json)
			VALUES (?, ?, ?, ?, 'seed', ?, ?, ?, ?, ?)`,
			ids.New(), sessionID, i, seed.Stance, seed.Title, seed.Summary,
			string(tasksJSON), string(risksJSON), string(assumptionsJSON)); err != nil {
			return fmt.Errorf("saving plan seed: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE plan_sessions SET stage = 'expanding', expanded_count = 0,
		 heartbeat_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, sessionID); err != nil {
		return fmt.Errorf("marking seeds saved: %w", err)
	}
	return tx.Commit()
}

// Progress persists the live run stage, model, sizes, tail, and heartbeat.
func (s *Store) Progress(ctx context.Context, sessionID, stage, model string, promptBytes, responseBytes int, tail string, tokens int) {
	_, _ = s.db.DB().ExecContext(ctx, `
		UPDATE plan_sessions SET
			stage = CASE WHEN ? <> '' THEN ? ELSE stage END,
			model = CASE WHEN ? <> '' THEN ? ELSE model END,
			prompt_bytes = MAX(prompt_bytes, ?),
			response_bytes = MAX(response_bytes, ?),
			tail = CASE WHEN ? <> '' THEN ? ELSE tail END,
			total_tokens = total_tokens + ?,
			heartbeat_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id = ?`,
		stage, stage, model, model, promptBytes, responseBytes, tail, tail, tokens, sessionID)
}

// MarkExpanding marks one seed as being expanded.
func (s *Store) MarkExpanding(ctx context.Context, sessionID string, index int) {
	_, _ = s.db.DB().ExecContext(ctx,
		`UPDATE plan_candidates SET status = 'expanding', error = ''
		  WHERE session_id = ? AND sort_order = ?`, sessionID, index)
}

// SaveExpanded replaces one seed row with its complete plan.
func (s *Store) SaveExpanded(ctx context.Context, sessionID string, index int, c Candidate) error {
	tasks, _ := json.Marshal(c.Tasks)
	risks, _ := json.Marshal(c.Risks)
	assumptions, _ := json.Marshal(c.Assumptions)
	if _, err := s.db.DB().ExecContext(ctx, `
		UPDATE plan_candidates SET stance = ?, status = 'ready', title = ?, summary = ?,
			tasks_json = ?, risks_json = ?, assumptions_json = ?, error = '',
			expanded_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE session_id = ? AND sort_order = ?`,
		c.Stance, c.Title, c.Summary, string(tasks), string(risks), string(assumptions), sessionID, index); err != nil {
		return fmt.Errorf("saving expanded plan: %w", err)
	}
	_, _ = s.db.DB().ExecContext(ctx, `
		UPDATE plan_sessions SET expanded_count =
			(SELECT COUNT(*) FROM plan_candidates WHERE session_id = ? AND status = 'ready'),
			heartbeat_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, sessionID, sessionID)
	return nil
}

// FailCandidate marks one expansion failed; the other plans remain usable.
func (s *Store) FailCandidate(ctx context.Context, sessionID string, index int, msg string) {
	_, _ = s.db.DB().ExecContext(ctx,
		`UPDATE plan_candidates SET status = 'failed', error = ?
		  WHERE session_id = ? AND sort_order = ?`, msg, sessionID, index)
}

// Finish marks the run done when at least one plan expanded; otherwise it
// fails the session. Failed individual plans remain visible for retry.
func (s *Store) Finish(ctx context.Context, sessionID string) error {
	var ready int
	if err := s.db.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM plan_candidates WHERE session_id = ? AND status = 'ready'`, sessionID).Scan(&ready); err != nil {
		return err
	}
	status, errMsg, stage := "ready", "", "done"
	if ready == 0 {
		status, errMsg, stage = "failed", "no plan expanded successfully; generate again", "failed"
	}
	_, err := s.db.DB().ExecContext(ctx,
		`UPDATE plan_sessions SET status = ?, error = ?, stage = ?, expanded_count = ?,
		 heartbeat_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
		status, errMsg, stage, ready, sessionID)
	return err
}

// InterruptStale marks generations whose process died as interrupted. It is
// called at boot; the page then offers an immediate retry instead of waiting
// for a long timeout.
func (s *Store) InterruptStale(ctx context.Context, olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan).UTC().Format("2006-01-02T15:04:05.000Z")
	var ids []string
	rows, err := s.db.DB().QueryContext(ctx,
		`SELECT id FROM plan_sessions WHERE status = 'generating'
		  AND (heartbeat_at IS NULL OR heartbeat_at < ?)`, cutoff)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE plan_sessions SET status = 'failed', stage = 'interrupted',
			 stop_reason = 'interrupted', error = 'plan generation was interrupted; generate again'
			 WHERE id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE plan_candidates SET status = 'failed', error = 'interrupted'
			 WHERE session_id = ? AND status IN ('seed','expanding')`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Fail(ctx context.Context, sessionID, msg string) {
	_, _ = s.db.DB().ExecContext(ctx,
		`UPDATE plan_sessions SET status = 'failed', error = ?, stop_reason = 'failed',
		 stage = 'failed', heartbeat_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, msg, sessionID)
}

func (s *Store) Get(ctx context.Context, projectID string) (Session, bool, error) {
	var id string
	err := s.db.DB().QueryRowContext(ctx, `
		SELECT id FROM plan_sessions WHERE project_id = ?
		ORDER BY created_at DESC, rowid DESC LIMIT 1`, projectID).Scan(&id)
	if err == sql.ErrNoRows {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	return s.getByID(ctx, id)
}

// getByID loads one session and its candidates by primary key, so Begin and
// Generate cannot accidentally return a different concurrent session.
func (s *Store) getByID(ctx context.Context, sessionID string) (Session, bool, error) {
	var sess Session
	var created, updated string
	var heartbeat sql.NullString
	err := s.db.DB().QueryRowContext(ctx, `
		SELECT id, project_id, option_id, status, error, stop_reason, stage, model,
		       prompt_bytes, response_bytes, tail, total_tokens, expanded_count,
		       heartbeat_at, selected_index, created_at, updated_at
		FROM plan_sessions WHERE id = ?`, sessionID).
		Scan(&sess.ID, &sess.ProjectID, &sess.OptionID, &sess.Status, &sess.Error,
			&sess.StopReason, &sess.Stage, &sess.Model, &sess.PromptBytes, &sess.ResponseBytes,
			&sess.Tail, &sess.TotalTokens, &sess.ExpandedCount, &heartbeat,
			&sess.SelectedIndex, &created, &updated)
	if err == sql.ErrNoRows {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	sess.CreatedAt, _ = time.Parse(time.RFC3339, created)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	if heartbeat.Valid {
		sess.HeartbeatAt, _ = time.Parse(time.RFC3339, heartbeat.String)
	}
	rows, err := s.db.DB().QueryContext(ctx, `
		SELECT stance, status, error, title, summary, tasks_json, risks_json, assumptions_json
		FROM plan_candidates WHERE session_id = ? ORDER BY sort_order`, sess.ID)
	if err != nil {
		return Session{}, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var c Candidate
		var tasks, risks, assumptions string
		if err := rows.Scan(&c.Stance, &c.Status, &c.Error, &c.Title, &c.Summary,
			&tasks, &risks, &assumptions); err != nil {
			return Session{}, false, err
		}
		if err := json.Unmarshal([]byte(tasks), &c.Tasks); err != nil {
			return Session{}, false, fmt.Errorf("planning: decoding tasks for %q: %w", c.Title, err)
		}
		if err := json.Unmarshal([]byte(risks), &c.Risks); err != nil {
			return Session{}, false, fmt.Errorf("planning: decoding risks for %q: %w", c.Title, err)
		}
		if err := json.Unmarshal([]byte(assumptions), &c.Assumptions); err != nil {
			return Session{}, false, fmt.Errorf("planning: decoding assumptions for %q: %w", c.Title, err)
		}
		sess.Candidates = append(sess.Candidates, c)
	}
	return sess, true, rows.Err()
}

func (s *Store) Select(ctx context.Context, projectID string, index int) error {
	sess, ok, err := s.Get(ctx, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("planning: no session for project")
	}
	if sess.Status != "ready" {
		return fmt.Errorf("planning: session is %s, not ready", sess.Status)
	}
	if index < 0 || index >= len(sess.Candidates) {
		return fmt.Errorf("planning: candidate index %d out of range", index)
	}
	if sess.Candidates[index].Status != "ready" {
		return fmt.Errorf("planning: plan %d is %s, not ready", index, sess.Candidates[index].Status)
	}
	_, err = s.db.DB().ExecContext(ctx,
		`UPDATE plan_sessions SET selected_index = ? WHERE id = ?`, index, sess.ID)
	return err
}

// Service ties a generator to the store.
type Service struct {
	gen   Generator
	store *Store
}

func NewService(gen Generator, st *Store) *Service { return &Service{gen: gen, store: st} }

const (
	seedCallTimeout   = 8 * time.Minute
	expandCallTimeout = 8 * time.Minute
)

func (s *Service) Generate(ctx context.Context, projectID, optionID string, req Request) (Session, error) {
	if s.gen == nil {
		return Session{}, fmt.Errorf("planning: no generator configured")
	}
	sess, err := s.store.Begin(ctx, projectID, optionID)
	if err != nil {
		return Session{}, err
	}
	fail := func(err error) (Session, error) {
		fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.store.Fail(fctx, sess.ID, err.Error())
		return Session{}, err
	}

	// One observer wraps the caller's callback and persists the live stage so
	// a restart can show a truthful state.
	pinnedModel, pinnedTarget := "", 0
	report := func(ev ProgressEvent) {
		if ev.Model != "" {
			pinnedModel = ev.Model
		}
		if ev.ContextTarget > 0 {
			pinnedTarget = ev.ContextTarget
		}
		pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.store.Progress(pctx, sess.ID, stageLabel(ev), ev.Model, ev.PromptBytes, ev.ResponseBytes, ev.Tail, ev.Tokens)
		cancel()
		if req.Progress != nil {
			req.Progress(ev)
		}
	}

	// Call 1: three seeds, one per stance.
	seedReq := req
	seedReq.Phase = "seed"
	seedReq.Progress = report
	var seeds []Seed
	var seedErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			seedReq.Repair = seedErr.Error()
			report(ProgressEvent{Phase: "seed", Stage: "repair", Detail: "repairing the plan seeds"})
		}
		callCtx, cancel := context.WithTimeout(ctx, seedCallTimeout)
		raw, genErr := s.gen.Generate(callCtx, seedReq)
		cancel()
		if genErr != nil {
			return fail(genErr)
		}
		seeds, seedErr = ParseSeeds(raw)
		if seedErr == nil {
			seeds, seedErr = ValidateSeeds(seeds)
		}
		if seedErr == nil {
			break
		}
		report(ProgressEvent{Phase: "seed", Stage: "rejected", Detail: seedErr.Error()})
	}
	if seedErr != nil {
		return fail(seedErr)
	}
	if err := s.store.SaveSeeds(ctx, sess.ID, seeds); err != nil {
		return fail(err)
	}
	report(ProgressEvent{Phase: "seed", Stage: "seeds", Detail: "three plan seeds ready", Candidates: 3})

	// Calls 2–4: one expansion per seed, once each, with one contract repair.
	for i, seed := range seeds {
		s.store.MarkExpanding(ctx, sess.ID, i)
		others := make([]Seed, 0, len(seeds)-1)
		for j, other := range seeds {
			if j != i {
				others = append(others, other)
			}
		}
		expandReq := req
		expandReq.Phase = "expand"
		expandReq.Seed = &seed
		expandReq.OtherSeeds = others
		expandReq.Model = pinnedModel
		expandReq.ContextTarget = pinnedTarget
		expandReq.Progress = func(ev ProgressEvent) {
			ev.PlanIndex = i + 1
			report(ev)
		}
		report(ProgressEvent{Phase: "expand", Stage: "expand", Detail: "expanding " + seed.Stance, PlanIndex: i + 1})
		var expanded Candidate
		var expandErr error
		for attempt := 0; attempt < 2; attempt++ {
			if attempt > 0 {
				expandReq.Repair = expandErr.Error()
				report(ProgressEvent{Phase: "expand", Stage: "repair", Detail: "repairing plan " + seed.Stance, PlanIndex: i + 1})
			}
			callCtx, cancel := context.WithTimeout(ctx, expandCallTimeout)
			raw, genErr := s.gen.Generate(callCtx, expandReq)
			cancel()
			if genErr != nil {
				expandErr = genErr
				break
			}
			candidate, parseErr := ParseOne(raw)
			if parseErr == nil {
				candidate, parseErr = ValidateExpanded(candidate, seed)
			}
			if parseErr == nil {
				expanded = candidate
				expandErr = nil
				break
			}
			expandErr = parseErr
			report(ProgressEvent{Phase: "expand", Stage: "rejected", Detail: parseErr.Error(), PlanIndex: i + 1})
		}
		if expandErr != nil {
			s.store.FailCandidate(ctx, sess.ID, i, expandErr.Error())
			report(ProgressEvent{Phase: "expand", Stage: "failed", Detail: expandErr.Error(), PlanIndex: i + 1})
			continue
		}
		if err := s.store.SaveExpanded(ctx, sess.ID, i, expanded); err != nil {
			return fail(err)
		}
		report(ProgressEvent{Phase: "expand", Stage: "expanded", Detail: "plan " + seed.Stance + " ready", PlanIndex: i + 1, Candidates: i + 1})
	}
	if err := s.store.Finish(ctx, sess.ID); err != nil {
		return fail(err)
	}
	got, ok, err := s.store.getByID(ctx, sess.ID)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, fmt.Errorf("planning: session disappeared after completion")
	}
	return got, nil
}

func stageLabel(ev ProgressEvent) string {
	if ev.Phase == "" {
		return ev.Stage
	}
	if ev.PlanIndex > 0 {
		return fmt.Sprintf("%s:%d:%s", ev.Phase, ev.PlanIndex, ev.Stage)
	}
	return ev.Phase + ":" + ev.Stage
}

// InterruptStale marks plan runs whose process died as interrupted. The UI
// calls this on boot; a stale run is immediately retryable.
func (s *Service) InterruptStale(ctx context.Context, olderThan time.Duration) error {
	return s.store.InterruptStale(ctx, olderThan)
}

func (s *Service) Get(ctx context.Context, projectID string) (Session, bool, error) {
	return s.store.Get(ctx, projectID)
}

func (s *Service) Select(ctx context.Context, projectID string, index int) error {
	return s.store.Select(ctx, projectID, index)
}

// ToTaskSpecs converts a chosen candidate into dependency-ordered project
// tasks. Each task depends on the previous one, which is the smallest safe
// graph for an ordered plan; a future planner may emit explicit edges.
func ToTaskSpecs(c Candidate) []project.TaskSpec {
	hasDeps := false
	for _, t := range c.Tasks {
		if len(t.DependsOn) > 0 {
			hasDeps = true
			break
		}
	}
	specs := make([]project.TaskSpec, 0, len(c.Tasks))
	for i, t := range c.Tasks {
		key := strings.TrimSpace(t.Key)
		if key == "" {
			key = fmt.Sprintf("task-%d", i+1)
		}
		var deps []string
		if hasDeps {
			deps = append([]string(nil), t.DependsOn...)
		} else if i > 0 {
			// A legacy plan without explicit edges is ordered linearly.
			deps = []string{fmt.Sprintf("task-%d", i)}
		}
		specs = append(specs, project.TaskSpec{
			Key: key, Title: t.Title, Description: t.Description,
			Capability: t.Capability, Criteria: t.Criteria, DependsOn: deps,
		})
	}
	return specs
}
