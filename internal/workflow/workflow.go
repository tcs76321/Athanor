// Package workflow is the typed block graph behind a project cycle
// (ADR-0134). A cycle is a DAG of closed-repertoire blocks; this package
// validates the graph and compiles it to the project.TaskSpec values the
// existing scheduler already executes.
//
// It deliberately does not execute anything: blocks are typed data, the
// graph is the contract, and execution stays in internal/engine +
// internal/scheduler. Adding a second executor here would fork the state
// machine.
package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tcs76321/athanor/internal/dag"
	"github.com/tcs76321/athanor/internal/project"
)

// Kind is one block in the closed repertoire. The model composes these; it
// does not define new ones (ADR-0079's no-plugin posture).
type Kind string

const (
	KindResearch        Kind = "research"
	KindFetch           Kind = "fetch"
	KindExtract         Kind = "extract"
	KindCondense        Kind = "condense"
	KindGenerateOptions Kind = "generate_options"
	KindCritique        Kind = "critique"
	KindSelect          Kind = "select"
	KindSynthesize      Kind = "synthesize"
	KindImplement       Kind = "implement"
	KindTest            Kind = "test"
	KindVerify          Kind = "verify"
	KindRetain          Kind = "retain"
	KindReview          Kind = "review"
	KindPublish         Kind = "publish"
)

// PortType is the closed data type carried on an edge.
type PortType string

const (
	PortText      PortType = "text"
	PortURLs      PortType = "urls"
	PortDocuments PortType = "documents"
	PortItems     PortType = "items"
	PortItem      PortType = "item"
	PortArtifact  PortType = "artifact"
	PortCode      PortType = "code"
	PortVerdict   PortType = "verdict"
	PortJSON      PortType = "json"
)

// Port is one typed input or output of a block kind.
type Port struct {
	Name     string   `json:"name"`
	Type     PortType `json:"type"`
	Required bool     `json:"required,omitempty"`
}

// BlockSpec is the registry entry for one kind. Ports are fixed per kind so
// the canvas and the compiler share one definition. Instruction and
// Acceptance are the block's execution contract: the compiler folds them into
// the task description and criteria, and the engine remains the executor.
type BlockSpec struct {
	Kind         Kind     `json:"kind"`
	Title        string   `json:"title"`
	Capability   string   `json:"capability"`
	Inputs       []Port   `json:"inputs"`
	Outputs      []Port   `json:"outputs"`
	DefaultTools []string `json:"default_tools,omitempty"`
	HumanGate    bool     `json:"human_gate,omitempty"`
	Instruction  string   `json:"instruction,omitempty"`
	Acceptance   []string `json:"acceptance,omitempty"`
}

// contract is what one block kind must produce. It is deliberately prompt-level
// work, not a second executor: the block tells the existing engine what outcome
// counts as done for this kind.
type contract struct {
	Instruction string
	Acceptance  []string
}

// contracts is the closed execution contract set. Keep the instructions about
// the *output*, not the process, so the engine can still choose its own method.
var contracts = map[Kind]contract{
	KindResearch: {
		Instruction: "Research the topic using only the allowed sources. Return a sourced brief: every claim names the source it came from (a URL or a local file name), and end with the list of sources actually used.",
		Acceptance:  []string{"every paragraph names its source", "the sources section lists the files used"},
	},
	KindFetch: {
		Instruction: "Fetch or read the listed sources. Return their key content with attribution: each passage names its source (URL or file name). Do not add claims the sources do not make.",
		Acceptance:  []string{"content is attributed to its source"},
	},
	KindExtract: {
		Instruction: "Extract the substantive items from the upstream material. One item per line, each with its source (URL or file name) and, where present, its date.",
		Acceptance:  []string{"one item per line", "every item names its source"},
	},
	KindCondense: {
		Instruction: "Condense the upstream items into a short set of findings. Keep source attribution, drop duplicates, and introduce no new claims.",
		Acceptance:  []string{"no claim beyond the upstream material", "every finding keeps its source"},
	},
	KindGenerateOptions: {
		Instruction: "Produce three materially distinct options. For each: what it is, who it is for, the first concrete step, and the main risk. Do not recommend one yet.",
		Acceptance:  []string{"three distinct options", "each option names a first concrete step and a risk"},
	},
	KindCritique: {
		Instruction: "Critique each upstream option against the goal. Name the strongest and the weakest, and the specific trade-off that separates them.",
		Acceptance:  []string{"names the strongest and weakest option", "names the deciding trade-off"},
	},
	KindSelect: {
		Instruction: "Recommend one upstream option. State the reason, the rejected alternatives, and what would change the choice. This is a human gate: propose, do not decide for the user.",
		Acceptance:  []string{"one recommendation with a reason", "the alternatives and the change condition are stated"},
	},
	KindSynthesize: {
		Instruction: "Synthesize the upstream material into the requested artifact. Preserve every citation and introduce no unsourced claim.",
		Acceptance:  []string{"every citation is preserved", "no unsourced claim"},
	},
	KindImplement: {
		Instruction: "Implement the requested change in the project repository. Keep the diff focused and reviewable; do not touch unrelated code.",
		Acceptance:  []string{"the change is focused and reviewable", "the project still builds"},
	},
	KindTest: {
		Instruction: "Run the project's tests and report the exact command, the pass/fail outcome, and the first failure with its file and line.",
		Acceptance:  []string{"the exact command and its outcome are reported"},
	},
	KindVerify: {
		Instruction: "Verify the upstream artifact against the acceptance criteria. Use the headings Verdict and Evidence. Mark each criterion met, not met, or unverifiable, and cite the evidence.",
		Acceptance:  []string{"every criterion is marked met, not met, or unverifiable"},
	},
	KindRetain: {
		Instruction: "Record what was covered. Use the headings Covered, Sources, and Date. List the items seen and the sources used, and record the date. This memory is what stops the next cycle repeating today's work.",
		Acceptance:  []string{"lists the items covered and the sources used", "records the date"},
	},
	KindReview: {
		Instruction: "Review the upstream artifact against the project frame. Recommend accept, revise, or reject, with reasons. This is a human gate: propose, do not decide for the user.",
		Acceptance:  []string{"recommendation is accept, revise, or reject", "reasons are given"},
	},
	KindPublish: {
		Instruction: "Prepare the upstream artifact for release. List every external or irreversible action this would take; take none of them without approval.",
		Acceptance:  []string{"external actions are listed before any is taken"},
	},
}

// specs is the closed repertoire. Keep it small and orthogonal; a new kind is
// a product decision, not a model output.
var specs = map[Kind]BlockSpec{
	KindResearch: {
		Kind: KindResearch, Title: "Research", Capability: project.CapabilityDocument,
		Inputs: []Port{{Name: "topic", Type: PortText, Required: true}},
		// Research both finds sources and leaves a written brief, so it can
		// feed a fetch chain or an option generator directly. The unique
		// compatible port pair decides an unnamed edge.
		Outputs:      []Port{{Name: "sources", Type: PortURLs}, {Name: "notes", Type: PortText}},
		DefaultTools: []string{"fetch_url", "search_web", "read_file"},
	},
	KindFetch: {
		Kind: KindFetch, Title: "Fetch", Capability: project.CapabilityDocument,
		Inputs:       []Port{{Name: "urls", Type: PortURLs, Required: true}},
		Outputs:      []Port{{Name: "documents", Type: PortDocuments}},
		DefaultTools: []string{"fetch_url", "read_file"},
	},
	KindExtract: {
		Kind: KindExtract, Title: "Extract", Capability: project.CapabilityData,
		Inputs:  []Port{{Name: "documents", Type: PortDocuments, Required: true}},
		Outputs: []Port{{Name: "items", Type: PortItems}},
	},
	KindCondense: {
		Kind: KindCondense, Title: "Condense", Capability: project.CapabilityDocument,
		Inputs:  []Port{{Name: "items", Type: PortItems, Required: true}},
		Outputs: []Port{{Name: "summaries", Type: PortItems}},
	},
	KindGenerateOptions: {
		Kind: KindGenerateOptions, Title: "Generate options", Capability: project.CapabilityText,
		Inputs:  []Port{{Name: "brief", Type: PortText, Required: true}},
		Outputs: []Port{{Name: "options", Type: PortItems}},
	},
	KindCritique: {
		Kind: KindCritique, Title: "Critique", Capability: project.CapabilityDocument,
		Inputs:  []Port{{Name: "options", Type: PortItems, Required: true}},
		Outputs: []Port{{Name: "comparisons", Type: PortItems}},
	},
	KindSelect: {
		Kind: KindSelect, Title: "Select", Capability: project.CapabilityText,
		Inputs:    []Port{{Name: "options", Type: PortItems, Required: true}},
		Outputs:   []Port{{Name: "chosen", Type: PortItem}},
		HumanGate: true,
	},
	KindSynthesize: {
		Kind: KindSynthesize, Title: "Synthesize", Capability: project.CapabilityDocument,
		Inputs:  []Port{{Name: "items", Type: PortItems, Required: true}},
		Outputs: []Port{{Name: "artifact", Type: PortArtifact}},
	},
	KindImplement: {
		Kind: KindImplement, Title: "Implement", Capability: project.CapabilityCode,
		Inputs:       []Port{{Name: "spec", Type: PortText, Required: true}},
		Outputs:      []Port{{Name: "code", Type: PortCode}},
		DefaultTools: []string{"execute_code", "run_tests", "lint"},
	},
	KindTest: {
		Kind: KindTest, Title: "Test", Capability: project.CapabilityCode,
		Inputs:       []Port{{Name: "code", Type: PortCode, Required: true}},
		Outputs:      []Port{{Name: "verdict", Type: PortVerdict}},
		DefaultTools: []string{"run_tests"},
	},
	KindVerify: {
		Kind: KindVerify, Title: "Verify", Capability: project.CapabilityDocument,
		Inputs:  []Port{{Name: "artifact", Type: PortArtifact, Required: true}},
		Outputs: []Port{{Name: "verdict", Type: PortVerdict}},
	},
	KindRetain: {
		Kind: KindRetain, Title: "Retain", Capability: project.CapabilityDocument,
		// Retain accepts whichever upstream form exists: a selected item, an
		// item list, or a finished artifact. The unique compatible pair picks
		// the right one.
		Inputs: []Port{
			{Name: "item", Type: PortItem},
			{Name: "items", Type: PortItems},
			{Name: "artifact", Type: PortArtifact},
		},
		Outputs: []Port{{Name: "memory", Type: PortJSON}},
	},
	KindReview: {
		Kind: KindReview, Title: "Review", Capability: project.CapabilityDocument,
		Inputs: []Port{
			{Name: "artifact", Type: PortArtifact},
			{Name: "verdict", Type: PortVerdict},
		},
		Outputs:   []Port{{Name: "decision", Type: PortVerdict}},
		HumanGate: true,
	},
	KindPublish: {
		Kind: KindPublish, Title: "Publish", Capability: project.CapabilityDocument,
		Inputs:  []Port{{Name: "artifact", Type: PortArtifact, Required: true}},
		Outputs: []Port{{Name: "published", Type: PortJSON}},
	},
}

// Registry returns the closed repertoire in deterministic kind order, with
// each kind's execution contract attached.
func Registry() []BlockSpec {
	out := make([]BlockSpec, 0, len(specs))
	for _, s := range specs {
		if c, ok := contracts[s.Kind]; ok {
			s.Instruction, s.Acceptance = c.Instruction, c.Acceptance
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// Spec resolves one kind, with its execution contract attached.
func Spec(kind Kind) (BlockSpec, bool) {
	s, ok := specs[kind]
	if !ok {
		return s, false
	}
	if c, ok := contracts[kind]; ok {
		s.Instruction, s.Acceptance = c.Instruction, c.Acceptance
	}
	return s, true
}

// Block is one node in a cycle graph. Config carries block-specific settings
// (sources, limits, instructions, acceptance) and is validated at execution
// time, not here.
type Block struct {
	ID     string         `json:"id"`
	Kind   Kind           `json:"kind"`
	Title  string         `json:"title,omitempty"`
	Config map[string]any `json:"config,omitempty"`
}

// Edge is a typed dependency. An empty FromPort/ToPort is allowed only when
// the source/target kind has exactly one output/input.
type Edge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	FromPort string `json:"from_port,omitempty"`
	ToPort   string `json:"to_port,omitempty"`
}

// Graph is the cycle graph the user and Athanor refine.
type Graph struct {
	Blocks []Block `json:"blocks"`
	Edges  []Edge  `json:"edges"`
}

// Limits bounds a graph so a model cannot propose an unbounded workflow.
type Limits struct {
	MaxBlocks int
	MaxDepth  int
}

// DefaultLimits is the 0.x bound.
func DefaultLimits() Limits { return Limits{MaxBlocks: 32, MaxDepth: 12} }

// ValidationError names the graph location that failed.
type ValidationError struct {
	Path string
	Msg  string
}

func (e *ValidationError) Error() string {
	if e.Path == "" {
		return "workflow: " + e.Msg
	}
	return "workflow: " + e.Path + ": " + e.Msg
}

// Validate checks the closed repertoire, unique IDs, typed edges, acyclicity,
// and the graph bounds. It is pure.
func Validate(g Graph, limits Limits) error {
	if limits.MaxBlocks <= 0 {
		limits.MaxBlocks = DefaultLimits().MaxBlocks
	}
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = DefaultLimits().MaxDepth
	}
	if len(g.Blocks) == 0 {
		return &ValidationError{Msg: "graph has no blocks"}
	}
	if len(g.Blocks) > limits.MaxBlocks {
		return &ValidationError{Msg: fmt.Sprintf("graph has %d blocks, max %d", len(g.Blocks), limits.MaxBlocks)}
	}
	byID := make(map[string]Block, len(g.Blocks))
	for _, b := range g.Blocks {
		if strings.TrimSpace(b.ID) == "" {
			return &ValidationError{Msg: "block has an empty id"}
		}
		if _, dup := byID[b.ID]; dup {
			return &ValidationError{Path: b.ID, Msg: "duplicate block id"}
		}
		spec, ok := Spec(b.Kind)
		if !ok {
			return &ValidationError{Path: b.ID, Msg: fmt.Sprintf("unknown block kind %q", b.Kind)}
		}
		if base := strings.ToLower(b.ID); base != b.ID || strings.ContainsAny(b.ID, " \t\n") {
			return &ValidationError{Path: b.ID, Msg: "block id must be lowercase and contain no whitespace"}
		}
		_ = spec
		byID[b.ID] = b
	}
	seenEdge := map[string]bool{}
	for i, e := range g.Edges {
		from, ok := byID[e.From]
		if !ok {
			return &ValidationError{Path: fmt.Sprintf("edges[%d]", i), Msg: fmt.Sprintf("unknown from block %q", e.From)}
		}
		to, ok := byID[e.To]
		if !ok {
			return &ValidationError{Path: fmt.Sprintf("edges[%d]", i), Msg: fmt.Sprintf("unknown to block %q", e.To)}
		}
		if e.From == e.To {
			return &ValidationError{Path: fmt.Sprintf("edges[%d]", i), Msg: "self edge"}
		}
		fromSpec, _ := Spec(from.Kind)
		toSpec, _ := Spec(to.Kind)
		fp, tp, err := resolveEdgePorts(fromSpec.Outputs, toSpec.Inputs, e.FromPort, e.ToPort, e.From, e.To)
		if err != nil {
			return &ValidationError{Path: fmt.Sprintf("edges[%d]", i), Msg: err.Error()}
		}
		key := e.From + "\x00" + e.To + "\x00" + fp.Name + "\x00" + tp.Name
		if seenEdge[key] {
			return &ValidationError{Path: fmt.Sprintf("edges[%d]", i), Msg: "duplicate edge"}
		}
		seenEdge[key] = true
	}
	if err := validateAcyclic(g, byID); err != nil {
		return err
	}
	if depth := longestPath(g, byID); depth > limits.MaxDepth {
		return &ValidationError{Msg: fmt.Sprintf("graph depth %d exceeds %d", depth, limits.MaxDepth)}
	}
	return nil
}

// Normalize validates a graph and rewrites every edge to its concrete,
// compatible port pair. Callers persist the normalized graph, so the stored
// graph and the canvas show the real wiring instead of a model's guess. A
// graph that cannot be resolved is returned as an error and is never stored.
func Normalize(g Graph) (Graph, error) {
	if err := Validate(g, DefaultLimits()); err != nil {
		return Graph{}, err
	}
	edges := make([]Edge, len(g.Edges))
	copy(edges, g.Edges)
	g.Edges = edges
	byID := make(map[string]Block, len(g.Blocks))
	for _, b := range g.Blocks {
		byID[b.ID] = b
	}
	for i := range g.Edges {
		fromSpec, _ := Spec(byID[g.Edges[i].From].Kind)
		toSpec, _ := Spec(byID[g.Edges[i].To].Kind)
		fp, tp, err := resolveEdgePorts(fromSpec.Outputs, toSpec.Inputs,
			g.Edges[i].FromPort, g.Edges[i].ToPort, g.Edges[i].From, g.Edges[i].To)
		if err != nil {
			return Graph{}, &ValidationError{Path: fmt.Sprintf("edges[%d]", i), Msg: err.Error()}
		}
		g.Edges[i].FromPort = fp.Name
		g.Edges[i].ToPort = tp.Name
	}
	return g, nil
}

// resolveEdgePorts picks the typed output/input pair an edge carries. Ports
// are wiring derived from the closed repertoire, not a user contract, so a
// model's plausible-but-wrong port name is repaired from the compatible pairs
// between the two blocks rather than discarding an otherwise sound graph:
// exactly one pair is used, zero is reported, and more than one is ambiguous.
// This keeps natural compositions like research.notes -> generate_options.brief
// and critique.comparisons -> select.options working.
func resolveEdgePorts(outputs, inputs []Port, fromName, toName, fromID, toID string) (Port, Port, error) {
	var fp, tp Port
	fromKnown, toKnown := fromName == "", toName == ""
	if fromName != "" {
		if p, ok := findPort(outputs, fromName); ok {
			fp, fromKnown = p, true
		}
	}
	if toName != "" {
		if p, ok := findPort(inputs, toName); ok {
			tp, toKnown = p, true
		}
	}
	if (fromName != "" || toName != "") && fromKnown && toKnown && fp.Type == tp.Type {
		return fp, tp, nil
	}
	requestedMismatch := fromKnown && toKnown && fp.Type != tp.Type
	// An omitted, unknown, or type-incompatible side does not restrict the
	// search; a known and compatible side does.
	filterFrom, filterTo := "", ""
	if fromKnown && !requestedMismatch {
		filterFrom = fromName
	}
	if toKnown && !requestedMismatch {
		filterTo = toName
	}
	type pair struct{ out, in Port }
	var candidates []pair
	for _, o := range outputs {
		if filterFrom != "" && o.Name != filterFrom {
			continue
		}
		for _, in := range inputs {
			if filterTo != "" && in.Name != filterTo {
				continue
			}
			if o.Type == in.Type {
				candidates = append(candidates, pair{o, in})
			}
		}
	}
	switch len(candidates) {
	case 0:
		if !fromKnown {
			return Port{}, Port{}, fmt.Errorf("unknown output port %q on %s", fromName, fromID)
		}
		if !toKnown {
			return Port{}, Port{}, fmt.Errorf("unknown input port %q on %s", toName, toID)
		}
		if requestedMismatch {
			return Port{}, Port{}, fmt.Errorf("type mismatch: %s.%s is %s, %s.%s is %s, and no compatible pair exists",
				fromID, fp.Name, fp.Type, toID, tp.Name, tp.Type)
		}
		return Port{}, Port{}, fmt.Errorf("type mismatch: no compatible port pair between %s and %s (%s)",
			fromID, toID, describePorts(outputs, inputs))
	case 1:
		return candidates[0].out, candidates[0].in, nil
	default:
		if requestedMismatch {
			return Port{}, Port{}, fmt.Errorf("ambiguous edge from %s to %s: requested %s->%s is incompatible and %d other compatible pairs exist",
				fromID, toID, fp.Name, tp.Name, len(candidates))
		}
		return Port{}, Port{}, fmt.Errorf("ambiguous edge from %s to %s: %d compatible port pairs; specify from_port/to_port",
			fromID, toID, len(candidates))
	}
}

func findPort(ports []Port, name string) (Port, bool) {
	for _, p := range ports {
		if p.Name == name {
			return p, true
		}
	}
	return Port{}, false
}

func describePorts(outputs, inputs []Port) string {
	part := func(ports []Port) string {
		names := make([]string, 0, len(ports))
		for _, p := range ports {
			names = append(names, p.Name+":"+string(p.Type))
		}
		return strings.Join(names, ", ")
	}
	return "outputs " + part(outputs) + "; inputs " + part(inputs)
}

func validateAcyclic(g Graph, byID map[string]Block) error {
	indegree := make(map[string]int, len(g.Blocks))
	adj := make(map[string][]string, len(g.Blocks))
	for id := range byID {
		indegree[id] = 0
	}
	for _, e := range g.Edges {
		adj[e.From] = append(adj[e.From], e.To)
		indegree[e.To]++
	}
	queue := make([]string, 0, len(g.Blocks))
	for id, n := range indegree {
		if n == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	seen := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		seen++
		for _, next := range adj[id] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if seen != len(g.Blocks) {
		return &ValidationError{Msg: "graph has a cycle"}
	}
	return nil
}

func longestPath(g Graph, byID map[string]Block) int {
	indegree := make(map[string]int, len(g.Blocks))
	adj := make(map[string][]string, len(g.Blocks))
	for id := range byID {
		indegree[id] = 0
	}
	for _, e := range g.Edges {
		adj[e.From] = append(adj[e.From], e.To)
		indegree[e.To]++
	}
	dist := make(map[string]int, len(g.Blocks))
	queue := make([]string, 0, len(g.Blocks))
	for id, n := range indegree {
		if n == 0 {
			dist[id] = 1
			queue = append(queue, id)
		}
	}
	max := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if dist[id] > max {
			max = dist[id]
		}
		for _, next := range adj[id] {
			if dist[id]+1 > dist[next] {
				dist[next] = dist[id] + 1
			}
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	return max
}

// CompileOptions carries the cycle identity and any defaults the caller owns.
type CompileOptions struct {
	CycleID string
	// Sources are the project's declared source names. They are folded into
	// the research and fetch blocks' descriptions, which is where the engine's
	// research sub-step reads URLs from.
	Sources []string
	// SourceTexts carries the local source documents (name -> text) read from
	// the project repository. The model gets the authoritative text in the
	// task prompt, so a local-first source does not depend on a tool call.
	SourceTexts map[string]string
}

// Compile validates the graph and returns TaskSpecs in topological order. The
// block ID becomes the task key; an edge from A to B becomes B's dependency on
// A. The existing scheduler is the executor.
func Compile(g Graph, opts CompileOptions) ([]project.TaskSpec, error) {
	if err := Validate(g, DefaultLimits()); err != nil {
		return nil, err
	}
	byID := make(map[string]Block, len(g.Blocks))
	for _, b := range g.Blocks {
		byID[b.ID] = b
	}
	order := topoOrder(g)
	deps := make(map[string][]string, len(g.Blocks))
	for _, e := range g.Edges {
		deps[e.To] = append(deps[e.To], e.From)
	}
	out := make([]project.TaskSpec, 0, len(order))
	for _, id := range order {
		b := byID[id]
		spec, _ := Spec(b.Kind)
		title := strings.TrimSpace(b.Title)
		if title == "" {
			title = spec.Title
		}
		out = append(out, project.TaskSpec{
			Key:          b.ID,
			Title:        title,
			Description:  description(b, spec, opts),
			DependsOn:    append([]string(nil), deps[id]...),
			Criteria:     criteria(b.Config, spec.Acceptance),
			Budget:       dag.Budget{},
			Capability:   spec.Capability,
			AllowedTools: append([]string(nil), spec.DefaultTools...),
			CycleID:      opts.CycleID,
			Checks:       blockChecks(b),
		})
	}
	return out, nil
}

// blockChecks compiles a block's deterministic acceptance. A block may carry
// an explicit `checks` map (same vocabulary as project execution checks);
// otherwise the kind gets a structural default so an advisory judge cannot
// wedge a cycle. Synthesize blocks are expected to declare their own headings
// in the template; without one, the block falls back to non-empty structural
// bounds.
func blockChecks(b Block) *project.ExecutionChecks {
	if raw, ok := b.Config["checks"]; ok && raw != nil {
		data, err := json.Marshal(raw)
		if err == nil {
			var checks project.ExecutionChecks
			if err := json.Unmarshal(data, &checks); err == nil && !checks.IsZero() {
				return &checks
			}
		}
	}
	switch b.Kind {
	case KindResearch, KindFetch, KindExtract, KindCondense:
		return &project.ExecutionChecks{MinWords: 8, MaxWords: 4000}
	case KindVerify:
		return &project.ExecutionChecks{
			MustContainAny: []string{"met", "not met", "unverifiable"},
			MinWords:       5,
			MaxWords:       800,
		}
	case KindRetain:
		return &project.ExecutionChecks{
			MustContain: []string{"Sources", "Date"},
			MinWords:    5,
			MaxWords:    800,
		}
	}
	return nil
}

// description folds the block's contract, the operator's instructions, and the
// project's sources into the task description. The description is the pinned
// task section of every engine prompt, so this is where a block's semantics
// reach the executor. An explicit instruction never erases the contract: it
// adds task-specific direction after it.
func description(b Block, spec BlockSpec, opts CompileOptions) string {
	parts := make([]string, 0, 3)
	if spec.Instruction != "" {
		parts = append(parts, spec.Instruction)
	}
	if s, ok := b.Config["instructions"].(string); ok && strings.TrimSpace(s) != "" {
		parts = append(parts, s)
	}
	if b.Kind == KindResearch {
		if qs := configStrings(b.Config["queries"]); len(qs) > 0 {
			parts = append(parts, "SEARCH QUERIES:\n- "+strings.Join(qs, "\n- "))
		}
	}
	if (b.Kind == KindResearch || b.Kind == KindFetch) && len(opts.Sources) > 0 {
		var sb strings.Builder
		sb.WriteString("Sources to use:")
		for _, src := range opts.Sources {
			sb.WriteString("\n- ")
			sb.WriteString(src)
		}
		parts = append(parts, sb.String())
	}
	if (b.Kind == KindResearch || b.Kind == KindFetch) && len(opts.SourceTexts) > 0 {
		var sb strings.Builder
		sb.WriteString("SOURCE MATERIAL (authoritative; cite the file name, do not invent facts):")
		names := make([]string, 0, len(opts.SourceTexts))
		for name := range opts.SourceTexts {
			names = append(names, name)
		}
		sort.Strings(names)
		total := 0
		for _, name := range names {
			text := opts.SourceTexts[name]
			if strings.TrimSpace(text) == "" {
				continue
			}
			if len(text) > 6000 {
				text = text[:6000] + "…"
			}
			if total+len(text) > 16000 {
				break
			}
			sb.WriteString("\n\n--- ")
			sb.WriteString(name)
			sb.WriteString(" ---\n")
			sb.WriteString(strings.TrimSpace(text))
			total += len(text)
		}
		if total > 0 {
			parts = append(parts, sb.String())
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%s block (%s).", spec.Title, b.Kind)
	}
	return strings.Join(parts, "\n\n")
}

// criteria returns the operator's acceptance override, or the block kind's
// default criteria when none is set.
func criteria(config map[string]any, defaults []string) []string {
	if config == nil {
		return append([]string(nil), defaults...)
	}
	raw, ok := config["acceptance"]
	if !ok {
		return append([]string(nil), defaults...)
	}
	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// configStrings reads a string list from a block's free-form config. Both
// []string (YAML/programmatic) and []any (JSON round-trip) are accepted.
func configStrings(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return nil
	}
}

func topoOrder(g Graph) []string {
	indegree := make(map[string]int, len(g.Blocks))
	adj := make(map[string][]string, len(g.Blocks))
	for _, b := range g.Blocks {
		indegree[b.ID] = 0
	}
	for _, e := range g.Edges {
		adj[e.From] = append(adj[e.From], e.To)
		indegree[e.To]++
	}
	queue := make([]string, 0, len(g.Blocks))
	for _, b := range g.Blocks {
		if indegree[b.ID] == 0 {
			queue = append(queue, b.ID)
		}
	}
	sort.Strings(queue)
	out := make([]string, 0, len(g.Blocks))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		out = append(out, id)
		next := append([]string(nil), adj[id]...)
		sort.Strings(next)
		for _, n := range next {
			indegree[n]--
			if indegree[n] == 0 {
				queue = append(queue, n)
			}
		}
	}
	return out
}

// TriggerKind is how a cycle starts.
type TriggerKind string

const (
	TriggerManual   TriggerKind = "manual"
	TriggerSchedule TriggerKind = "schedule"
	TriggerEvent    TriggerKind = "event"
)

// Trigger starts a cycle. Spec is kind-specific (a cron expression, an event
// name, or empty for manual).
type Trigger struct {
	ID      string      `json:"id"`
	Kind    TriggerKind `json:"kind"`
	Spec    string      `json:"spec,omitempty"`
	Enabled bool        `json:"enabled"`
}

// Template is a versioned cycle graph plus its triggers.
type Template struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"version"`
	// CycleKind is the timeline role the template produces: discovery, design,
	// build, operate, or review. It is a plain string so this package stays
	// free of the cycle repository; the caller maps it.
	CycleKind string    `json:"cycle_kind"`
	Graph     Graph     `json:"graph"`
	Triggers  []Trigger `json:"triggers,omitempty"`
}

// ValidateTemplate validates the graph, the trigger kinds, and the cycle kind.
func ValidateTemplate(t Template) error {
	if strings.TrimSpace(t.ID) == "" || strings.TrimSpace(t.Name) == "" {
		return &ValidationError{Msg: "template id and name are required"}
	}
	if t.Version < 1 {
		return &ValidationError{Msg: "template version must be >= 1"}
	}
	switch t.CycleKind {
	case "discovery", "design", "build", "operate", "review":
	default:
		return &ValidationError{Msg: fmt.Sprintf("unknown cycle kind %q", t.CycleKind)}
	}
	if err := Validate(t.Graph, DefaultLimits()); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, tr := range t.Triggers {
		if tr.ID == "" {
			return &ValidationError{Path: fmt.Sprintf("triggers[%d]", i), Msg: "trigger id is required"}
		}
		if seen[tr.ID] {
			return &ValidationError{Path: fmt.Sprintf("triggers[%d]", i), Msg: "duplicate trigger id"}
		}
		seen[tr.ID] = true
		switch tr.Kind {
		case TriggerManual, TriggerSchedule, TriggerEvent:
		default:
			return &ValidationError{Path: fmt.Sprintf("triggers[%d]", i), Msg: fmt.Sprintf("unknown trigger kind %q", tr.Kind)}
		}
	}
	return nil
}

// EncodeTemplate serializes a template as canonical JSON.
func EncodeTemplate(t Template) ([]byte, error) {
	if err := ValidateTemplate(t); err != nil {
		return nil, err
	}
	return json.Marshal(t)
}

// DecodeTemplate parses and validates a template.
func DecodeTemplate(raw []byte) (Template, error) {
	var t Template
	if err := json.Unmarshal(raw, &t); err != nil {
		return Template{}, fmt.Errorf("workflow: decoding template: %w", err)
	}
	if err := ValidateTemplate(t); err != nil {
		return Template{}, err
	}
	return t, nil
}
