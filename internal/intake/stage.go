package intake

import (
	"context"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/discovery"
	"github.com/tcs76321/athanor/internal/planning"
	"github.com/tcs76321/athanor/internal/project"
)

const (
	statusRunning = "running"
	statusDone    = "done"
	statusFailed  = "failed"
	maxRuns       = 64
)

// Stage is the derived intake state. It is never persisted; it is a pure
// function of the project, its direction session, its plan session, and
// whether a live run exists.
type Stage string

const (
	StageIntent     Stage = "intent"
	StageDirections Stage = "directions"
	StageChoose     Stage = "choose"
	StagePlan       Stage = "plan"
	StageWorking    Stage = "working"
)

// Step is one observable generation step.
type Step struct {
	Stage  string
	Detail string
	At     time.Time
}

// Run is the live view of one discovery or planning pass. The durable session
// is the source of truth; Run only makes a long model call visible.
type Run struct {
	Status        string
	Stage         string
	Detail        string
	Model         string
	Error         string
	StartedAt     time.Time
	UpdatedAt     time.Time
	PromptBytes   int
	ResponseBytes int
	Options       int
	PlanIndex     int
	Expanded      int
	Tail          string
	Steps         []Step
}

// State is everything an intake page or API client needs.
type State struct {
	Project        project.Project
	Stage          Stage
	Directions     discovery.Session
	HasDirections  bool
	DirectionRun   *Run
	DirectionError string
	Plan           planning.Session
	HasPlan        bool
	PlanRun        *Run
	PlanError      string
}

// State loads and evaluates a project's intake state.
func (s *Service) State(ctx context.Context, projectID string) (State, error) {
	p, err := s.deps.Projects.Get(ctx, projectID)
	if err != nil {
		return State{}, err
	}
	st := State{Project: p}
	tasks, err := s.deps.Projects.TasksByProject(ctx, projectID)
	if err != nil {
		return State{}, err
	}
	if len(tasks) > 0 {
		st.Stage = StageWorking
		return st, nil
	}
	if s.deps.Discovery != nil {
		sess, ok, derr := s.deps.Discovery.Get(ctx, projectID)
		if derr != nil {
			return State{}, derr
		}
		st.Directions, st.HasDirections = sess, ok
	}
	if s.deps.Planning != nil {
		sess, ok, perr := s.deps.Planning.Get(ctx, projectID)
		if perr != nil {
			return State{}, perr
		}
		st.Plan, st.HasPlan = sess, ok
	}
	st.DirectionRun = s.RunFor(projectID)
	st.PlanRun = s.PlanRunFor(projectID)
	evaluate(&st, len(tasks) > 0)
	return st, nil
}

// evaluate is the pure stage function. It is exported through State and
// covered directly by stage_test.go.
func evaluate(st *State, hasTasks bool) {
	if hasTasks {
		st.Stage = StageWorking
		return
	}
	p := st.Project
	selected := selectedOption(st.Directions, st.HasDirections)
	if p.OnRamp == project.OnRampDirect {
		evaluatePlan(st, nil)
		return
	}
	// Explore (or an unset on-ramp on a legacy row: the session presence is
	// the truthful signal there).
	switch {
	case !st.HasDirections:
		if st.DirectionRun != nil && st.DirectionRun.Status == statusRunning {
			st.Stage = StageDirections
			return
		}
		st.Stage = StageIntent
		return
	case st.Directions.Status == "generating":
		if st.DirectionRun != nil && st.DirectionRun.Status == statusRunning {
			st.Stage = StageDirections
			return
		}
		st.Stage = StageIntent
		st.DirectionError = "The previous discovery run was interrupted. Start it again when ready."
		return
	case st.Directions.Status == "failed":
		st.Stage = StageIntent
		st.DirectionError = st.Directions.Error
		return
	case selected == nil:
		st.Stage = StageChoose
		if len(st.Directions.Options) == 0 {
			st.DirectionError = "No complete directions were returned. Generate them again."
		}
		return
	}
	evaluatePlan(st, selected)
}

// evaluatePlan decides the plan half of the flow. A plan session generated
// for a different direction is stale and ignored.
func evaluatePlan(st *State, selected *discovery.Option) {
	if st.PlanRun != nil && st.PlanRun.Status == statusRunning {
		st.Stage = StagePlan
		return
	}
	if !st.HasPlan {
		st.Stage = StagePlan
		return
	}
	if selected != nil && st.Plan.OptionID != "" && st.Plan.OptionID != selected.ID {
		st.HasPlan = false
		st.Plan = planning.Session{}
		st.Stage = StagePlan
		return
	}
	switch st.Plan.Status {
	case "generating":
		st.Stage = StagePlan
		st.PlanError = "The previous plan pass was interrupted. Generate again when ready."
	case "failed":
		st.Stage = StagePlan
		st.PlanError = st.Plan.Error
		if st.Plan.StopReason == "interrupted" {
			st.PlanError = "The previous plan pass was interrupted. Generate again when ready."
		}
	default:
		st.Stage = StagePlan
		if len(st.Plan.Candidates) == 0 {
			st.PlanError = "No complete plan was returned. Generate again when ready."
		}
	}
}

func selectedOption(sess discovery.Session, ok bool) *discovery.Option {
	if !ok || sess.SelectedID == "" {
		return nil
	}
	for i := range sess.Options {
		if sess.Options[i].ID == sess.SelectedID {
			return &sess.Options[i]
		}
	}
	return nil
}

// RunFor returns a copy of the live direction run.
func (s *Service) RunFor(projectID string) *Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyRun(s.runs[projectID])
}

// PlanRunFor returns a copy of the live plan run.
func (s *Service) PlanRunFor(projectID string) *Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyRun(s.plans[projectID])
}

func copyRun(run *Run) *Run {
	if run == nil {
		return nil
	}
	cp := *run
	cp.Steps = append([]Step(nil), run.Steps...)
	return &cp
}

// Cancel stops an in-flight direction or plan pass.
func (s *Service) Cancel(projectID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel := s.cancels[projectID]; cancel != nil {
		cancel()
	}
}

func (s *Service) reportRun(projectID string, ev discovery.ProgressEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[projectID]
	if run == nil {
		return
	}
	now := time.Now()
	run.Stage = ev.Stage
	if ev.Model != "" {
		run.Model = ev.Model
	}
	if ev.Detail != "" {
		run.Detail = ev.Detail
	}
	run.UpdatedAt = now
	if ev.Stage == "prompt" {
		run.PromptBytes = ev.Bytes
	} else if ev.Bytes > 0 {
		run.ResponseBytes = ev.Bytes
	}
	if ev.Options > 0 {
		run.Options = ev.Options
	}
	if ev.Tail != "" {
		run.Tail = ev.Tail
	}
	appendStep(&run.Steps, ev.Stage, ev.Detail, now)
}

func (s *Service) reportPlan(projectID string, ev planning.ProgressEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.plans[projectID]
	if run == nil {
		return
	}
	now := time.Now()
	run.Stage = ev.Stage
	if ev.Detail != "" {
		run.Detail = ev.Detail
	}
	if ev.Model != "" {
		run.Model = ev.Model
	}
	if ev.PromptBytes > 0 {
		run.PromptBytes = ev.PromptBytes
	}
	if ev.ResponseBytes > run.ResponseBytes {
		run.ResponseBytes = ev.ResponseBytes
	}
	if ev.Tail != "" {
		run.Tail = ev.Tail
	}
	if ev.PlanIndex > 0 {
		run.PlanIndex = ev.PlanIndex
	}
	if ev.Candidates > run.Expanded {
		run.Expanded = ev.Candidates
	}
	run.UpdatedAt = now
	appendStep(&run.Steps, ev.Stage, ev.Detail, now)
}

func appendStep(steps *[]Step, stage, detail string, at time.Time) {
	if detail == "" {
		return
	}
	if n := len(*steps); n > 0 && (*steps)[n-1].Stage == stage && (*steps)[n-1].Detail == detail {
		return
	}
	*steps = append(*steps, Step{Stage: stage, Detail: detail, At: at})
	if len(*steps) > 20 {
		*steps = (*steps)[len(*steps)-20:]
	}
}

func (s *Service) finishRun(projectID, kind string, options int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[projectID]
	delete(s.cancels, projectID)
	if run == nil {
		return
	}
	run.UpdatedAt = time.Now()
	run.Options = options
	if err != nil {
		run.Status, run.Stage, run.Error = statusFailed, "failed", err.Error()
		if strings.Contains(err.Error(), "context canceled") {
			run.Detail, run.Error = kind+" cancelled", "cancelled by operator"
		}
		return
	}
	run.Status, run.Stage, run.Detail = statusDone, "done", kind+" ready"
}

func (s *Service) finishPlanRun(projectID string, candidates int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.plans[projectID]
	delete(s.cancels, projectID)
	if run == nil {
		return
	}
	run.UpdatedAt = time.Now()
	run.Expanded = candidates
	if err != nil {
		run.Status, run.Stage, run.Error = statusFailed, "failed", err.Error()
		if strings.Contains(err.Error(), "context canceled") {
			run.Detail, run.Error = "planning cancelled", "cancelled by operator"
		}
		return
	}
	run.Status, run.Stage, run.Detail = statusDone, "done", "plans ready"
}
