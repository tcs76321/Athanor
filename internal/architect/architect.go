// Package architect is the conversation-first project onboarding service
// (ADR-0134 §3). It turns a short conversation into a reviewed Project Frame
// and a Cycle 0 graph. It is deliberately pure over the model seam: no
// database, no project creation, no execution. The caller presents the
// proposal, the user approves or edits it, and only then is a cycle created.
package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/workflow"
)

// Message is one conversation turn.
type Message struct {
	Role    string `json:"role"` // user | assistant
	Content string `json:"content"`
}

// Frame is the reviewed project intent. Inferred fields are labeled so the
// user can correct them; they are never silently treated as user facts.
type Frame struct {
	Name         string   `json:"name"`
	Goal         string   `json:"goal"`
	Outcome      string   `json:"outcome,omitempty"`
	Repository   string   `json:"repository,omitempty"`
	Sources      []string `json:"sources,omitempty"`
	Constraints  []string `json:"constraints,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Acceptance   []string `json:"acceptance,omitempty"`
	FirstCycle   string   `json:"first_cycle"` // discovery | design | build
	ReviewPolicy string   `json:"review_policy,omitempty"`
	Inferred     []string `json:"inferred,omitempty"`
}

// Proposal is what the architect shows before approval.
type Proposal struct {
	Questions []string       `json:"questions,omitempty"`
	Frame     Frame          `json:"frame"`
	Graph     workflow.Graph `json:"graph"`
	Notes     string         `json:"notes,omitempty"`
	// Degraded is true when the model output could not be used and the
	// deterministic fallback supplied the proposal. The UI must say so.
	Degraded bool `json:"degraded,omitempty"`
}

// Ready reports whether the proposal can be approved without more answers.
func (p Proposal) Ready() bool {
	return len(p.Questions) == 0 && len(p.Graph.Blocks) > 0
}

// Completer is the model seam. The production adapter wraps internal/llm; the
// tests use a literal response.
type Completer interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// Service proposes frames and graphs.
type Service struct {
	LLM Completer
}

// New returns a service over the given model seam. A nil seam is valid and
// always returns the deterministic fallback.
func New(llm Completer) *Service { return &Service{LLM: llm} }

// Propose asks the model for a frame and graph, validates them, and falls back
// deterministically when the output is unusable. A model failure is a degraded
// proposal, not a failed onboarding: the user can still edit and approve.
func (s *Service) Propose(ctx context.Context, messages []Message) (Proposal, error) {
	if len(messages) == 0 {
		return Proposal{}, fmt.Errorf("architect: no messages")
	}
	lastUser := lastUserMessage(messages)
	if strings.TrimSpace(lastUser) == "" {
		return Proposal{}, fmt.Errorf("architect: no user message")
	}
	fallback := fallbackProposal(lastUser, messages)
	if s.LLM == nil {
		fallback.Degraded = true
		fallback.Notes = "No model was available; this is the deterministic starting proposal."
		return fallback, nil
	}
	raw, err := s.LLM.Complete(ctx, buildPrompt(messages))
	if err != nil {
		fallback.Degraded = true
		fallback.Notes = "The model call failed (" + err.Error() + "); this is the deterministic starting proposal."
		return fallback, nil
	}
	p, err := parseProposal(raw, lastUser)
	if err != nil {
		fallback.Degraded = true
		fallback.Notes = "The model response could not be used (" + err.Error() + "); this is the deterministic starting proposal."
		return fallback, nil
	}
	if err := workflow.Validate(p.Graph, workflow.DefaultLimits()); err != nil {
		fallback.Degraded = true
		fallback.Notes = "The proposed graph was invalid (" + err.Error() + "); this is the deterministic starting proposal."
		return fallback, nil
	}
	if len(p.Questions) > 5 {
		p.Questions = p.Questions[:5]
	}
	return p, nil
}

// buildPrompt gives the model the closed repertoire and the exact JSON shape.
// It asks for questions only where the answer changes the plan.
func buildPrompt(messages []Message) string {
	var b strings.Builder
	b.WriteString("You are Athanor's project architect. Turn the conversation into a Project Frame and a first-cycle graph.\n")
	b.WriteString("Ask at most five questions, and only where the answer changes the plan. If the user says you decide, decide cheaply and mark the field as inferred.\n")
	b.WriteString("Use only these block kinds and typed ports. Do not invent kinds. Keep the graph acyclic and at most 12 blocks.\n\n")
	for _, spec := range workflow.Registry() {
		fmt.Fprintf(&b, "- %s (%s): inputs", spec.Kind, spec.Title)
		for _, p := range spec.Inputs {
			fmt.Fprintf(&b, " %s:%s", p.Name, p.Type)
		}
		b.WriteString("; outputs")
		for _, p := range spec.Outputs {
			fmt.Fprintf(&b, " %s:%s", p.Name, p.Type)
		}
		if spec.HumanGate {
			b.WriteString("; human gate")
		}
		b.WriteString("\n")
	}
	b.WriteString("\nA block with a required input may take it from the cycle goal; an edge is needed only when one block feeds another.\n")
	b.WriteString("Return one JSON object only:\n")
	b.WriteString(`{"questions":["..."],"frame":{"name":"...","goal":"...","outcome":"...","repository":"","sources":[],"constraints":[],"capabilities":[],"acceptance":[],"first_cycle":"discovery|design|build","review_policy":"","inferred":[]},"graph":{"blocks":[{"id":"research","kind":"research","title":"","config":{}}],"edges":[{"from":"research","to":"fetch","from_port":"sources","to_port":"urls"}]},"notes":"..."}`)
	b.WriteString("\n\nCONVERSATION\n")
	for _, m := range messages {
		fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
	}
	return b.String()
}

func parseProposal(raw, lastUser string) (Proposal, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return Proposal{}, fmt.Errorf("no JSON object")
	}
	var p Proposal
	if err := json.Unmarshal([]byte(raw[start:end+1]), &p); err != nil {
		return Proposal{}, fmt.Errorf("decoding proposal: %w", err)
	}
	p.Graph = normalizeGraph(p.Graph)
	fillFrame(&p.Frame, lastUser, p.Questions)
	alignCycle(&p.Frame, p.Graph)
	if err := validateFrame(p.Frame); err != nil {
		return Proposal{}, err
	}
	return p, nil
}

// alignCycle keeps the frame's first cycle consistent with the graph's shape.
// A graph that ends in a human selection is a discovery cycle regardless of
// the label the model chose: the user is choosing a direction, not shipping.
func alignCycle(f *Frame, g workflow.Graph) {
	if f.FirstCycle == "discovery" {
		return
	}
	for _, b := range g.Blocks {
		if b.Kind == workflow.KindSelect {
			f.FirstCycle = "discovery"
			f.Inferred = appendUnique(f.Inferred, "first_cycle")
			return
		}
	}
}

func fillFrame(f *Frame, lastUser string, questions []string) {
	f.Goal = strings.TrimSpace(f.Goal)
	if f.Goal == "" {
		f.Goal = strings.TrimSpace(lastUser)
	}
	if strings.TrimSpace(f.Name) == "" {
		f.Name = shortName(f.Goal)
	}
	if f.Outcome == "" {
		f.Outcome = f.Goal
	}
	if len(f.Capabilities) == 0 {
		f.Capabilities = project.InferCapabilities(f.Goal+" "+f.Outcome, f.Repository)
		f.Capabilities = project.NormalizeCapabilities(f.Capabilities)
		f.Inferred = appendUnique(f.Inferred, "capabilities")
	}
	switch f.FirstCycle {
	case "discovery", "design", "build":
	default:
		f.FirstCycle = inferCycle(f.Goal, len(questions) > 0)
		f.Inferred = appendUnique(f.Inferred, "first_cycle")
	}
	if len(f.Acceptance) == 0 {
		f.Acceptance = defaultAcceptance(f.FirstCycle, f.Capabilities)
		f.Inferred = appendUnique(f.Inferred, "acceptance")
	}
	if f.ReviewPolicy == "" {
		f.ReviewPolicy = "internal work may proceed; external or irreversible actions require approval"
	}
	f.Inferred = dedupe(f.Inferred)
}

func validateFrame(f Frame) error {
	if strings.TrimSpace(f.Name) == "" {
		return fmt.Errorf("architect: frame name is required")
	}
	if strings.TrimSpace(f.Goal) == "" {
		return fmt.Errorf("architect: frame goal is required")
	}
	switch f.FirstCycle {
	case "discovery", "design", "build":
	default:
		return fmt.Errorf("architect: unknown first cycle %q", f.FirstCycle)
	}
	return nil
}

func inferCycle(goal string, hasQuestions bool) string {
	low := strings.ToLower(goal)
	if hasQuestions || containsAny(low, "not sure", "don't know", "do not know", "no idea", "idea", "explore", "suggest", "which", "what should") {
		return "discovery"
	}
	if containsAny(low, "code", "build", "implement", "fix", "api", "game", "app") {
		return "build"
	}
	return "design"
}

func defaultAcceptance(cycle string, caps []string) []string {
	switch cycle {
	case "discovery":
		return []string{"three distinct directions", "a selected direction with a reason", "an approved project frame"}
	case "build":
		return []string{"the project's tests pass", "the change is reviewable", "no external action without approval"}
	default:
		return []string{"the design is specific enough to implement", "alternatives and risks are named", "the user approves the direction"}
	}
}

// fallbackProposal is the deterministic starting point. It always produces a
// valid graph, so a model failure never blocks onboarding.
func fallbackProposal(goal string, messages []Message) Proposal {
	cycle := inferCycle(goal, false)
	f := Frame{Goal: strings.TrimSpace(goal), Outcome: strings.TrimSpace(goal)}
	fillFrame(&f, goal, nil)
	// A vague goal starts in discovery; a precise one starts in its inferred
	// cycle. This is the "I don't know what to make" path.
	if len(messages) <= 1 && containsAny(strings.ToLower(goal), "not sure", "don't know", "idea", "explore", "which", "what should") {
		f.FirstCycle = "discovery"
		cycle = "discovery"
	}
	if cycle == "discovery" {
		f.Acceptance = defaultAcceptance("discovery", f.Capabilities)
	}
	return Proposal{Frame: f, Graph: fallbackGraph(cycle), Notes: "Deterministic starting proposal."}
}

func fallbackGraph(cycle string) workflow.Graph {
	switch cycle {
	case "discovery":
		return workflow.Graph{
			Blocks: []workflow.Block{
				{ID: "options", Kind: workflow.KindGenerateOptions, Title: "Generate three directions"},
				{ID: "critique", Kind: workflow.KindCritique, Title: "Critique the directions"},
				{ID: "select", Kind: workflow.KindSelect, Title: "Choose a direction"},
			},
			Edges: []workflow.Edge{
				{From: "options", To: "critique"},
				{From: "critique", To: "select"},
			},
		}
	case "build":
		return workflow.Graph{
			Blocks: []workflow.Block{
				{ID: "implement", Kind: workflow.KindImplement, Title: "Implement the first slice"},
				{ID: "test", Kind: workflow.KindTest, Title: "Run the tests"},
			},
			Edges: []workflow.Edge{{From: "implement", To: "test"}},
		}
	default:
		return workflow.Graph{
			Blocks: []workflow.Block{
				{ID: "research", Kind: workflow.KindResearch, Title: "Research the brief"},
				{ID: "fetch", Kind: workflow.KindFetch, Title: "Fetch sources"},
				{ID: "extract", Kind: workflow.KindExtract, Title: "Extract facts"},
				{ID: "condense", Kind: workflow.KindCondense, Title: "Condense the facts"},
				{ID: "synthesize", Kind: workflow.KindSynthesize, Title: "Synthesize the artifact"},
				{ID: "verify", Kind: workflow.KindVerify, Title: "Verify citations and acceptance"},
			},
			Edges: []workflow.Edge{
				{From: "research", To: "fetch"},
				{From: "fetch", To: "extract"},
				{From: "extract", To: "condense"},
				{From: "condense", To: "synthesize"},
				{From: "synthesize", To: "verify"},
			},
		}
	}
}

// normalizeGraph makes model IDs safe before validation: lowercase, spaces to
// hyphens, unique suffixes. It does not change kinds or edges.
func normalizeGraph(g workflow.Graph) workflow.Graph {
	seen := map[string]int{}
	idMap := map[string]string{}
	for i := range g.Blocks {
		old := g.Blocks[i].ID
		id := strings.ToLower(strings.TrimSpace(old))
		id = strings.ReplaceAll(id, "_", "-")
		id = strings.Join(strings.Fields(id), "-")
		if id == "" {
			id = fmt.Sprintf("block-%d", i+1)
		}
		if n := seen[id]; n > 0 {
			seen[id] = n + 1
			id = fmt.Sprintf("%s-%d", id, n+1)
		} else {
			seen[id] = 1
		}
		idMap[old] = id
		g.Blocks[i].ID = id
	}
	for i := range g.Edges {
		if v, ok := idMap[g.Edges[i].From]; ok {
			g.Edges[i].From = v
		}
		if v, ok := idMap[g.Edges[i].To]; ok {
			g.Edges[i].To = v
		}
	}
	return g
}

func lastUserMessage(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && strings.TrimSpace(messages[i].Content) != "" {
			return messages[i].Content
		}
	}
	return ""
}

func shortName(goal string) string {
	goal = strings.TrimSpace(goal)
	if len(goal) <= 48 {
		return goal
	}
	return strings.TrimSpace(goal[:48]) + "…"
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}
