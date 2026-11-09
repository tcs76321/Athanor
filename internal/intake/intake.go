// Package intake owns the project intake flow: one intent record, an inferred
// direct/explore on-ramp, a direction portfolio when the goal is ambiguous,
// one plan, and human approval. It is the single state machine behind the UI
// and the HTTP API; the UI must not own intake SQL.
package intake

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tcs76321/athanor/internal/dag"
	"github.com/tcs76321/athanor/internal/discovery"
	"github.com/tcs76321/athanor/internal/firstuse"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/planning"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// Discovery is the direction-portfolio surface. Satisfied by
// *discovery.Service. nil disables the explore on-ramp.
type Discovery interface {
	Generate(ctx context.Context, projectID string, req discovery.Request) (discovery.Session, error)
	Get(ctx context.Context, projectID string) (discovery.Session, bool, error)
	Select(ctx context.Context, projectID, optionID string) error
}

type discoveryResetter interface {
	Reset(ctx context.Context, projectID string) error
}

// Planning is the plan-generation surface. Satisfied by *planning.Service.
type Planning interface {
	Generate(ctx context.Context, projectID, optionID string, req planning.Request) (planning.Session, error)
	GenerateSingle(ctx context.Context, projectID, optionID string, req planning.Request) (planning.Session, error)
	Get(ctx context.Context, projectID string) (planning.Session, bool, error)
	Select(ctx context.Context, projectID string, index int) error
	SelectByID(ctx context.Context, projectID, candidateID string) error
}

// Scheduler starts the ready leaves of an approved plan.
type Scheduler interface {
	Start(ctx context.Context, goalID string) ([]string, error)
}

// Enqueuer starts a job in the fallback path when no scheduler is wired.
type Enqueuer interface{ Enqueue(jobID string) }

// Deps wires the intake service.
type Deps struct {
	Projects  *project.Repo
	Jobs      *job.Repository
	Discovery Discovery
	Planning  Planning
	Scheduler Scheduler
	Enqueuer  Enqueuer
	Events    *store.Store
	DAGLimits dag.Limits
	// SourceAllowList is the gateway allowlist. Nil means no filtering
	// (tests); production always supplies it, including an empty list.
	SourceAllowList []string
	// WaitForIndex is optional. The UI uses it to wait for the bounded
	// repository pre-index before the first approved job starts.
	WaitForIndex func(ctx context.Context, p project.Project) error
}

// Service owns intake state and the in-flight generation runs.
type Service struct {
	deps Deps

	mu      sync.Mutex
	runs    map[string]*Run
	plans   map[string]*Run
	cancels map[string]context.CancelFunc
}

// New builds an intake service.
func New(deps Deps) *Service {
	return &Service{
		deps: deps, runs: map[string]*Run{}, plans: map[string]*Run{},
		cancels: map[string]context.CancelFunc{},
	}
}

// ErrAlreadyApproved reports a second approval of an approved project.
var ErrAlreadyApproved = errors.New("project already has an approved plan")

// SaveIntent replaces the project's intent and context. It resets a stale
// direction portfolio when the goal or boundaries changed.
func (s *Service) SaveIntent(ctx context.Context, projectID string, in project.Intent) (project.Project, error) {
	before, err := s.deps.Projects.Get(ctx, projectID)
	if err != nil {
		return project.Project{}, err
	}
	p, err := s.deps.Projects.UpdateIntent(ctx, projectID, in)
	if err != nil {
		return project.Project{}, err
	}
	if before.Goal != p.Goal || before.Boundaries != p.Boundaries ||
		len(before.Sources) != len(p.Sources) || strings.Join(before.Sources, "\n") != strings.Join(p.Sources, "\n") {
		if resetter, ok := s.deps.Discovery.(discoveryResetter); ok {
			if err := resetter.Reset(ctx, projectID); err != nil {
				slog.Warn("intake: clearing stale directions after intent change", "project", projectID, "err", err)
			}
		}
	}
	s.audit(ctx, projectID, "intent_updated", map[string]any{
		"onramp": p.OnRamp, "goal_changed": before.Goal != p.Goal,
	})
	return p, nil
}

// SetOnRamp records an operator override.
func (s *Service) SetOnRamp(ctx context.Context, projectID, onRamp string) error {
	if err := s.deps.Projects.SetOnRamp(ctx, projectID, onRamp); err != nil {
		return err
	}
	s.audit(ctx, projectID, "onramp_overridden", map[string]any{"onramp": onRamp})
	return nil
}

// StartDirections launches one discovery pass. It returns immediately; poll
// State for progress. The durable portfolio is written when the pass ends.
func (s *Service) StartDirections(ctx context.Context, projectID string) error {
	if s.deps.Discovery == nil {
		return fmt.Errorf("discovery is not configured")
	}
	p, err := s.deps.Projects.Get(ctx, projectID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if run := s.runs[projectID]; run != nil && run.Status == statusRunning {
		s.mu.Unlock()
		return fmt.Errorf("directions are already being generated")
	}
	run := &Run{Status: statusRunning, Stage: "starting", Detail: "preparing discovery", StartedAt: time.Now(), UpdatedAt: time.Now()}
	runCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	s.runs[projectID] = run
	s.cancels[projectID] = cancel
	s.mu.Unlock()

	s.audit(ctx, projectID, "discovery_started", map[string]any{
		"onramp": p.OnRamp, "sources": len(p.Sources),
	})
	req := discovery.Request{
		Goal: p.Goal, Boundaries: p.Boundaries, Sources: FilterSources(p.Sources, s.deps.SourceAllowList), Count: 10,
		Progress: func(ev discovery.ProgressEvent) { s.reportRun(projectID, ev) },
	}
	go func() {
		defer cancel()
		sess, err := s.deps.Discovery.Generate(runCtx, projectID, req)
		s.finishRun(projectID, "discovery", len(sess.Options), err)
	}()
	return nil
}

// StartPlans launches plan generation. mode is "single" for the direct
// on-ramp; empty uses the project's on-ramp. Explore requires a selected
// direction.
func (s *Service) StartPlans(ctx context.Context, projectID, mode string) error {
	if s.deps.Planning == nil {
		return fmt.Errorf("planning is not configured")
	}
	p, err := s.deps.Projects.Get(ctx, projectID)
	if err != nil {
		return err
	}
	option, _, err := s.selectedDirection(ctx, projectID)
	if err != nil {
		return err
	}
	if p.OnRamp != project.OnRampDirect && option == nil {
		return fmt.Errorf("choose a direction first")
	}
	s.mu.Lock()
	if run := s.plans[projectID]; run != nil && run.Status == statusRunning {
		s.mu.Unlock()
		return fmt.Errorf("plans are already being generated")
	}
	single := mode == "single" || (mode == "" && p.OnRamp == project.OnRampDirect)
	detail := "preparing the plan"
	if !single {
		detail = "preparing plan candidates"
	}
	run := &Run{Status: statusRunning, Stage: "starting", Detail: detail, StartedAt: time.Now(), UpdatedAt: time.Now()}
	runCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	s.plans[projectID] = run
	s.cancels[projectID] = cancel
	s.mu.Unlock()

	req := planning.Request{
		Goal: p.Goal, Boundaries: p.Boundaries, Sources: FilterSources(p.Sources, s.deps.SourceAllowList),
		Progress: func(ev planning.ProgressEvent) { s.reportPlan(projectID, ev) },
	}
	optionID := "direct"
	if option != nil {
		optionID = option.ID
		req.OptionTitle = option.Title
		req.Outcome = option.Outcome
		req.Method = option.Method
		req.Check = option.Check
		req.Scope = option.Scope
	}
	s.audit(ctx, projectID, "plan_started", map[string]any{
		"mode":   map[bool]string{true: "single", false: "portfolio"}[single],
		"option": optionID,
	})
	go func() {
		defer cancel()
		var sess planning.Session
		var genErr error
		if single {
			sess, genErr = s.deps.Planning.GenerateSingle(runCtx, projectID, optionID, req)
		} else {
			sess, genErr = s.deps.Planning.Generate(runCtx, projectID, optionID, req)
		}
		s.finishPlanRun(projectID, len(sess.Candidates), genErr)
	}()
	return nil
}

// SelectDirection records the chosen direction and discards a plan generated
// for a different direction.
func (s *Service) SelectDirection(ctx context.Context, projectID, optionID string) error {
	if s.deps.Discovery == nil {
		return fmt.Errorf("discovery is not configured")
	}
	if err := s.deps.Discovery.Select(ctx, projectID, optionID); err != nil {
		return err
	}
	s.audit(ctx, projectID, "direction_selected", map[string]any{"option": optionID})
	return nil
}

// SelectPlan records the chosen plan candidate.
func (s *Service) SelectPlan(ctx context.Context, projectID, candidateID string) error {
	if s.deps.Planning == nil {
		return fmt.Errorf("planning is not configured")
	}
	if err := s.deps.Planning.SelectByID(ctx, projectID, candidateID); err != nil {
		return err
	}
	s.audit(ctx, projectID, "plan_selected", map[string]any{"candidate": candidateID})
	return nil
}

// ApproveResult is the approved plan's execution graph.
type ApproveResult struct {
	GoalID  string
	Tasks   []project.Task
	JobIDs  []string
	PlanID  string
	Started bool
}

// Approve creates the approved plan's task graph and starts it. candidateID
// is empty for the deterministic fallback or for a session where the operator
// selected a candidate by index. It is idempotent only in the sense that a
// project with an approved plan returns ErrAlreadyApproved.
func (s *Service) Approve(ctx context.Context, projectID, candidateID string) (ApproveResult, error) {
	p, err := s.deps.Projects.Get(ctx, projectID)
	if err != nil {
		return ApproveResult{}, err
	}
	p.Sources = FilterSources(p.Sources, s.deps.SourceAllowList)
	if p.ApprovedPlanID != "" {
		return ApproveResult{}, ErrAlreadyApproved
	}
	option, _, err := s.selectedDirection(ctx, projectID)
	if err != nil {
		return ApproveResult{}, err
	}
	if p.OnRamp != project.OnRampDirect && option == nil {
		return ApproveResult{}, fmt.Errorf("choose a direction before approving the plan")
	}
	var specs []project.TaskSpec
	var planID, goalText string
	if s.deps.Planning != nil {
		ps, has, perr := s.deps.Planning.Get(ctx, projectID)
		if perr != nil {
			return ApproveResult{}, fmt.Errorf("reading the selected plan: %w", perr)
		}
		if has && ps.OptionID != "" && option != nil && ps.OptionID != option.ID {
			has = false
		}
		if has && ps.Status == "ready" && len(ps.Candidates) > 0 && candidateID != "deterministic" {
			cand, ok := chooseCandidate(ps, candidateID)
			if !ok {
				return ApproveResult{}, fmt.Errorf("choose a plan before approving")
			}
			if cand.Status != "ready" {
				return ApproveResult{}, fmt.Errorf("the selected plan is %s, not ready", cand.Status)
			}
			specs = planning.ToTaskSpecs(cand)
			planID = cand.ID
			goalText = fmt.Sprintf("%s: %s", cand.Title, cand.Summary)
		}
	}
	if len(specs) == 0 {
		if option == nil {
			if p.OnRamp != project.OnRampDirect {
				return ApproveResult{}, fmt.Errorf("no plan is available to approve")
			}
			option = defaultDirection(p)
		}
		_, specs = BuildDeterministicPlan(p, *option)
		planID = "deterministic"
		goalText = fmt.Sprintf("%s: %s", option.Title, option.Outcome)
	}
	if err := ValidateGraph(specs, s.deps.DAGLimits); err != nil {
		return ApproveResult{}, fmt.Errorf("the plan is not a valid task graph: %w", err)
	}
	if len(specs) > 0 {
		allowed := FilterSources(p.Sources, s.deps.SourceAllowList)
		if len(allowed) > 0 {
			specs[0].Description += " Allowed sources (fetch only these): " + strings.Join(allowed, " ")
			specs[0].AllowedTools = withSourceAccess(specs[0].Capability, specs[0].AllowedTools)
		}
	}
	if strings.TrimSpace(goalText) == "" {
		goalText = p.Goal
	}
	if len(goalText) > 500 {
		goalText = goalText[:500]
	}
	if len(goalText) < 3 {
		goalText = p.Goal
	}
	if err := s.deps.Projects.UpdateCapabilities(ctx, projectID, capabilitySet(p, specs)); err != nil {
		return ApproveResult{}, fmt.Errorf("updating capabilities: %w", err)
	}
	if s.deps.WaitForIndex != nil {
		if err := s.deps.WaitForIndex(ctx, p); err != nil {
			return ApproveResult{}, err
		}
	}
	goalID, tasks, err := s.deps.Projects.ApprovePlan(ctx, projectID, goalText, planID, specs)
	if err != nil {
		return ApproveResult{}, fmt.Errorf("creating the approved plan: %w", err)
	}
	res := ApproveResult{GoalID: goalID, Tasks: tasks, PlanID: planID, Started: true}
	if s.deps.Scheduler != nil {
		jobs, serr := s.deps.Scheduler.Start(ctx, goalID)
		if serr != nil {
			return ApproveResult{}, fmt.Errorf("starting the plan: %w", serr)
		}
		res.JobIDs = jobs
	} else if len(tasks) > 0 && s.deps.Enqueuer != nil {
		jobID, jerr := firstuse.StartTaskJob(ctx, s.deps.Events, s.deps.Projects, s.deps.Jobs, s.deps.Enqueuer, tasks[0].ID, projectID)
		if jerr != nil {
			// A nil job repository is not usable here; the caller should
			// wire a scheduler. Keep the approved graph and report the
			// start failure rather than silently dropping it.
			return ApproveResult{}, fmt.Errorf("starting the first task: %w", jerr)
		}
		res.JobIDs = []string{jobID}
	}
	s.audit(ctx, projectID, "plan_approved", map[string]any{
		"plan": planID, "goal": goalID, "tasks": len(tasks), "jobs": len(res.JobIDs),
	})
	return res, nil
}

// selectedDirection loads the newest direction session and the selected
// option. A missing session is not an error.
func (s *Service) selectedDirection(ctx context.Context, projectID string) (*discovery.Option, *discovery.Session, error) {
	if s.deps.Discovery == nil {
		return nil, nil, nil
	}
	sess, ok, err := s.deps.Discovery.Get(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, nil
	}
	for i := range sess.Options {
		if sess.Options[i].ID == sess.SelectedID {
			return &sess.Options[i], &sess, nil
		}
	}
	return nil, &sess, nil
}

func chooseCandidate(sess planning.Session, candidateID string) (planning.Candidate, bool) {
	if candidateID != "" {
		for _, c := range sess.Candidates {
			if c.ID == candidateID {
				return c, true
			}
		}
		return planning.Candidate{}, false
	}
	if sess.SelectedIndex >= 0 && sess.SelectedIndex < len(sess.Candidates) {
		return sess.Candidates[sess.SelectedIndex], true
	}
	if len(sess.Candidates) == 1 {
		return sess.Candidates[0], true
	}
	return planning.Candidate{}, false
}

// defaultDirection is the synthetic direction for a direct project that has
// no discovery portfolio. It never invents a fact: the goal is the outcome,
// and the check is the deterministic capability check.
func defaultDirection(p project.Project) *discovery.Option {
	return &discovery.Option{
		Title:   p.Name,
		Outcome: p.Goal,
		Method:  "Follow the project's established workflow and repository conventions.",
		Scope:   p.Boundaries,
		Check:   defaultCheck(p),
	}
}

func defaultCheck(p project.Project) string {
	switch p.PrimaryCapability() {
	case project.CapabilityCode:
		if cmd := p.TestCommand(); cmd != "" {
			return fmt.Sprintf("The repository's test command (%s) passes and the build is clean.", cmd)
		}
		return "The build succeeds and the changed behavior is covered by a passing check."
	case project.CapabilityData:
		return "The output parses and satisfies the declared data shape and checks."
	case project.CapabilityMedia:
		return "The media file opens, matches the requested format and dimensions, and is reviewable."
	default:
		return "The deliverable contains the required content, no placeholders, and every claim is traceable."
	}
}

func capabilitySet(p project.Project, specs []project.TaskSpec) []string {
	caps := append([]string(nil), p.Capabilities...)
	for _, spec := range specs {
		if project.ValidCapability(spec.Capability) {
			caps = append(caps, spec.Capability)
		}
	}
	return caps
}

// ValidateGraph applies the same structural rules as the autonomous
// decomposer to an approved plan.
func ValidateGraph(specs []project.TaskSpec, limits dag.Limits) error {
	nodes := make([]dag.Node, 0, len(specs))
	for _, s := range specs {
		nodes = append(nodes, dag.Node{
			Key: s.Key, ParentKey: s.ParentKey, Title: s.Title,
			Description: s.Description, DependsOn: s.DependsOn,
			Criteria: s.Criteria, Budget: s.Budget,
		})
	}
	_, err := dag.Validate(dag.Graph{Nodes: nodes}, limits)
	return err
}

// BuildDeterministicPlan is the zero-cost fallback: the chosen direction's
// outcome, method, and check become a three-task ordered plan.
func BuildDeterministicPlan(p project.Project, o discovery.Option) (string, []project.TaskSpec) {
	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n\n", o.Title)
	fmt.Fprintf(&out, "**Project:** %s\n\n", p.Name)
	fmt.Fprintf(&out, "## Outcome\n\n%s\n\n", o.Outcome)
	fmt.Fprintf(&out, "## Method\n\n%s\n", o.Method)
	if strings.TrimSpace(o.Scope) != "" {
		fmt.Fprintf(&out, "\n## Scope\n\n%s\n", o.Scope)
	}
	fmt.Fprintf(&out, "\n## Check\n\n%s\n", o.Check)
	if strings.TrimSpace(p.Boundaries) != "" {
		fmt.Fprintf(&out, "\n## Limits\n\n%s\n", p.Boundaries)
	}
	criteria := []string{}
	if strings.TrimSpace(o.Check) != "" {
		criteria = append(criteria, "The check passes: "+strings.TrimSpace(o.Check))
	}
	sourceLine := ""
	if len(p.Sources) > 0 {
		sourceLine = " Allowed sources (fetch only these): " + strings.Join(p.Sources, " ")
	}
	primary := project.PrimaryCapability(p.Capabilities)
	deliverable := project.InferTaskCapability(o.Title+" "+o.Outcome, p.Capabilities, p.RepositoryPath)
	baseline := project.CapabilityText
	baselineTitle := "Establish the baseline"
	baselineDescription := fmt.Sprintf("Read the material for %q. Record the current state, what each source contributes, what is missing, and any conflicting claims. Cite only the allowed sources.%s", o.Title, sourceLine)
	switch primary {
	case project.CapabilityData:
		baseline = project.CapabilityData
	case project.CapabilityCode:
		baseline = project.CapabilityCode
		baselineTitle = "Inspect the code and constraints"
		baselineDescription = fmt.Sprintf("Read the relevant files, run the existing checks, and record constraints, gaps, and anything that must not break for %q.%s", o.Title, sourceLine)
	}
	baselineCriteria := "A baseline note names the current state, the relevant source or files, and the gaps that remain."
	switch primary {
	case project.CapabilityCode:
		baselineCriteria = "An inspection note names the relevant files, the existing checks, and the constraints that must not break."
	case project.CapabilityData:
		baselineCriteria = "A source inventory names each input, its format, and the fields that must be normalized."
	}
	specs := []project.TaskSpec{
		{
			Key: "baseline", Title: baselineTitle, Capability: baseline,
			Description: baselineDescription,
			Criteria:    []string{baselineCriteria},
		},
		{
			Key: "deliverable", Title: "Produce the deliverable", Capability: deliverable,
			Description: fmt.Sprintf("Produce the outcome for %s: %s. Method: %s. Follow the project's limits and do not broaden the scope.%s", o.Title, o.Outcome, o.Method, sourceLine),
			DependsOn:   []string{"baseline"},
			Criteria:    criteria,
		},
		{
			Key: "verify", Title: "Verify against the check", Capability: project.CapabilityDocument,
			Description: fmt.Sprintf("Verify the deliverable against the check: %s. Record gaps, weak evidence, and anything that would mislead a reader. Do not rewrite the deliverable unless a gap requires it.", o.Check),
			DependsOn:   []string{"deliverable"},
			Criteria:    criteria,
		},
	}
	return out.String(), specs
}

func withSourceAccess(capability string, tools []string) []string {
	if capability == project.CapabilityCode {
		for _, tool := range project.CodeArchetypeTools() {
			tools = appendTool(tools, tool)
		}
	}
	return appendTool(tools, "fetch_url")
}

func appendTool(list []string, name string) []string {
	for _, item := range list {
		if item == name {
			return list
		}
	}
	return append(list, name)
}

func (s *Service) audit(ctx context.Context, projectID, event string, data map[string]any) {
	if s.deps.Events == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["event"] = event
	actx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.deps.Events.AppendEvent(actx, store.Event{
		Category: "projects", ProjectID: projectID, Data: data,
	}); err != nil {
		slog.Error("intake: auditing project event", "project", projectID, "event", event, "err", err)
	}
}
