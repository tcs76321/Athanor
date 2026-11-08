package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"

	"github.com/tcs76321/athanor/internal/cognitive"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/policy"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/strategy"
)

// StrategySink is the §13.3 capture seam (M6-T10): a job's profile at start
// and its immutable outcome at end. Satisfied by *strategy.Repo; nil disables
// capture.
type StrategySink interface {
	CreateProfile(ctx context.Context, p strategy.Profile) (strategy.Profile, error)
	CreateOutcome(ctx context.Context, o strategy.Outcome) (strategy.Outcome, error)
}

// StrategyProfileFeatures is the optional update seam for task features that
// became known after planning (the difficulty hint). A sink that does not
// implement it keeps the start-of-job classification.
type StrategyProfileFeatures interface {
	SetTaskFeatures(ctx context.Context, jobID string, f strategy.TaskFeatures) error
}

// StrategyInsightSource is the §13.4 prompt channel (M6-T11): the statements
// of *active* insights. Proposed insights are excluded by the source itself,
// so they can never affect a prompt.
type StrategyInsightSource interface {
	ActiveStatements(ctx context.Context) ([]string, error)
}

// StrategyInsightLister is the F4-T7a feedback→policy seam: the full *active*
// insights so the engine can bias the persona plan. It is optional and
// type-asserted, so a statement-only source keeps working.
type StrategyInsightLister interface {
	ActiveInsights(ctx context.Context) ([]strategy.Insight, error)
}

// StrategyArmHistory is the ADR-0110 learned-selection seam: recent outcomes
// grouped by the candidate-count arm that actually ran. It is optional and
// type-asserted so a history source that only supplies RecentStats keeps
// working unchanged.
type StrategyArmHistory interface {
	CandidateStats(ctx context.Context, archetype string, limit int) ([]strategy.CandidateStat, error)
}

// preferredDivergencePersona returns the divergence persona an active winning
// insight names for this archetype ("" when none applies). Only active
// insights count — a proposed insight stays inert, which is the §13.4
// contract.
func (e *Engine) preferredDivergencePersona(ctx context.Context, lister StrategyInsightLister, archetype string) string {
	insights, err := lister.ActiveInsights(ctx)
	if err != nil {
		slog.Warn("engine: reading active insights for policy bias", "err", err)
		return ""
	}
	pref := ""
	for _, ins := range insights {
		if ins.Polarity != strategy.PolarityWinning || ins.Pattern.Feature != "diverging.persona" {
			continue
		}
		if ins.Pattern.Context != "" && ins.Pattern.Context != "archetype="+archetype {
			continue
		}
		switch ins.Pattern.Value {
		case llm.RoleMain, llm.RoleAlternative, llm.RoleTall:
			pref = ins.Pattern.Value
		}
	}
	return pref
}

// phaseStrategies is the engine's fixed phase → persona mapping (the persona
// plan §13.1). Capture derives the profile from it with zero inference.
var phaseStrategies = []struct{ phase, role string }{
	{llm.PhasePlanning, llm.RoleTall},
	{llm.PhaseDiverging, llm.RoleMain},
	{llm.PhaseEvaluating, llm.RoleSecurity},
	{llm.PhaseReflecting, llm.RoleMain},
	{llm.PhaseSynthesizing, llm.RoleMain},
	{llm.PhaseComparing, llm.RoleSecurity},
}

// strategySignature builds the §13.3 signature: one entry per executed phase
// with its resolved persona and temperature. The persona is resolved through
// the plan's ModelRouting (F4-T1c), so a routed judge is recorded.
func (e *Engine) strategySignature(archetype string, plan policy.Plan) []strategy.SignatureEntry {
	out := make([]strategy.SignatureEntry, 0, len(phaseStrategies))
	for _, s := range phaseStrategies {
		role := e.roleFor(plan, s.phase, s.role)
		persona, ok := e.personaFor(context.Background(), s.phase, role, archetype, "", "", "")
		if !ok {
			continue
		}
		name := role
		if persona.Role == "pool" {
			name = persona.Model
		}
		entry := strategy.SignatureEntry{
			Phase:       s.phase,
			Persona:     name,
			Temperature: llm.ResolveTemperature(s.phase, persona.Temperature, nil),
		}
		if s.phase == llm.PhaseDiverging {
			entry.Candidates = plan.Candidates
		}
		out = append(out, entry)
	}
	return out
}

// captureProfile records the job's strategy profile at start (idempotent).
func (e *Engine) captureProfile(ctx context.Context, j job.Job) {
	if e.strategy == nil {
		return
	}
	p, t, err := e.contexts(ctx, j)
	if err != nil {
		slog.Error("engine: loading project for strategy profile", "job", j.ID, "err", err)
		return
	}
	plan := e.planFor(ctx, j, p, t)
	capability := project.CapabilityFor(p, t)
	e.auditComputePlan(ctx, j.ID, "profile", plan)
	if _, err := e.strategy.CreateProfile(ctx, strategy.Profile{
		JobID: j.ID, ProjectID: p.ID, Archetype: capability,
		Signature:    e.strategySignature(capability, plan),
		TaskFeatures: strategy.TaskFeatures{TaskType: t.TaskType},
	}); err != nil {
		slog.Error("engine: capturing strategy profile", "job", j.ID, "err", err)
	}
}

// updateProfileFeatures records features that only become known after
// planning. It is best-effort: a sink without the extension keeps the profile.
func (e *Engine) updateProfileFeatures(ctx context.Context, j job.Job, t project.Task) {
	sink, ok := e.strategy.(StrategyProfileFeatures)
	if !ok {
		return
	}
	f := strategy.TaskFeatures{TaskType: t.TaskType, DifficultyHint: e.state.Difficulty(ctx, j.ID)}
	if err := sink.SetTaskFeatures(ctx, j.ID, f); err != nil {
		slog.Warn("engine: updating strategy task features", "job", j.ID, "err", err)
	}
}

// captureOutcome records the immutable strategy outcome at terminal state.
func (e *Engine) captureOutcome(ctx context.Context, jobID string) {
	j, err := e.jobs.Get(ctx, jobID)
	if err != nil {
		slog.Error("engine: loading job for strategy outcome", "job", jobID, "err", err)
		return
	}
	if !j.State.Terminal() {
		return
	}
	// ADR-0104: feed the grounded outcome back to the composition pool. Only
	// a completed job counts as success; failure/cancel are evidence against
	// the cast that produced them.
	if e.selector != nil {
		e.selector.Observe(ctx, jobID, j.State == job.StateCompleted)
	}
	if e.strategy == nil {
		return
	}
	tokens, confidence := e.eventStats(ctx, jobID)
	stats := e.armStats(ctx, jobID)
	o := strategy.Outcome{
		JobID:               jobID,
		Result:              e.outcomeResult(ctx, j),
		Score:               e.maxEvaluationScore(ctx, jobID),
		EvaluatorConfidence: confidence,
		Retries:             e.taskRetries(ctx, j),
		ReflectionLoops:     e.reflectionLoops(ctx, jobID),
		Candidates:          e.firstCycleCandidates(ctx, jobID),
		TokenCost:           tokens,
		ThinkArm:            stats.arm,
		ReasoningTokens:     stats.reasoning,
		DeliberateUnits:     stats.deliberate,
		Refines:             stats.refines,
		Reframes:            stats.reframes,
	}
	if j.StartedAt != nil && j.FinishedAt != nil {
		o.WallTime = j.FinishedAt.Sub(*j.StartedAt)
	}
	o.Operations = e.trajectory(ctx, jobID)
	if _, err := e.strategy.CreateOutcome(ctx, o); err != nil {
		slog.Error("engine: capturing strategy outcome", "job", jobID, "err", err)
	}
}

// outcomeResult maps the terminal job state and the comparison winner to a
// §13.3 result.
func (e *Engine) outcomeResult(ctx context.Context, j job.Job) string {
	switch j.State {
	case job.StateFailed:
		return strategy.ResultFailed
	case job.StateCancelled:
		return strategy.ResultCancelled
	}
	winner := ""
	if events, err := e.db.QueryEvents(ctx, store.EventFilter{JobID: j.ID, Category: "jobs"}); err == nil {
		for _, ev := range events {
			var d struct {
				Event  string `json:"event"`
				Winner string `json:"winner"`
			}
			if json.Unmarshal([]byte(ev.DataJSON), &d) == nil && d.Event == "comparison" {
				winner = d.Winner
			}
		}
	}
	switch winner {
	case "new":
		return strategy.ResultAcceptedNew
	case "previous":
		return strategy.ResultAcceptedPrevious
	default:
		return strategy.ResultRejected
	}
}

// eventStats sums token usage across the job's `llm_call` rows and returns
// the last comparison confidence.
func (e *Engine) eventStats(ctx context.Context, jobID string) (tokens int, confidence float64) {
	events, err := e.db.QueryEvents(ctx, store.EventFilter{JobID: jobID, Category: "jobs"})
	if err != nil {
		return 0, 0
	}
	for _, ev := range events {
		var d struct {
			Event            string  `json:"event"`
			PromptTokens     int     `json:"prompt_tokens"`
			CompletionTokens int     `json:"completion_tokens"`
			ReasoningTokens  int     `json:"reasoning_tokens"`
			Confidence       float64 `json:"confidence"`
		}
		if json.Unmarshal([]byte(ev.DataJSON), &d) != nil {
			continue
		}
		switch d.Event {
		case "llm_call":
			// CompletionTokens (Ollama eval_count) already includes hidden
			// reasoning. ReasoningTokens is an estimate for telemetry, not an
			// additional token count.
			tokens += d.PromptTokens + d.CompletionTokens
		case "comparison":
			confidence = d.Confidence
		}
	}
	return tokens, confidence
}

// jobArmStats is the ADR-0090 S4 tag derived from a job's event log.
type jobArmStats struct {
	arm        string
	reasoning  int
	deliberate int
	refines    int
	reframes   int
}

// armStats derives the dominant generation think arm, the estimated reasoning
// spend, the proposed deliberate-unit count, and the regulator's moves.
func (e *Engine) armStats(ctx context.Context, jobID string) jobArmStats {
	out := jobArmStats{}
	events, err := e.db.QueryEvents(ctx, store.EventFilter{JobID: jobID, Category: "jobs"})
	if err != nil {
		return out
	}
	counts := map[string]int{}
	for _, ev := range events {
		var d struct {
			Event           string          `json:"event"`
			Think           json.RawMessage `json:"think"`
			ReasoningTokens int             `json:"reasoning_tokens"`
			Deliberate      int             `json:"deliberate"`
			Decision        string          `json:"decision"`
		}
		if json.Unmarshal([]byte(ev.DataJSON), &d) != nil {
			continue
		}
		switch d.Event {
		case "llm_call":
			out.reasoning += d.ReasoningTokens
			counts[thinkArm(d.Think)]++
		case "task_analysis":
			out.deliberate = d.Deliberate
		case "regulator_decision":
			switch d.Decision {
			case "refine":
				out.refines++
			case "reframe":
				out.reframes++
			}
		}
	}
	best := -1
	for arm, n := range counts {
		if n > best || (n == best && arm < out.arm) {
			out.arm, best = arm, n
		}
	}
	return out
}

// thinkArm renders the audit `think` value as a stable arm name.
func thinkArm(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return "unset"
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		if b {
			return "on"
		}
		return "off"
	}
	return "unset"
}

// maxEvaluationScore returns the highest score among the job's evaluation
// records (0 when the evaluation repo is not wired).
func (e *Engine) maxEvaluationScore(ctx context.Context, jobID string) float64 {
	if e.eval == nil {
		return 0
	}
	recs, err := e.eval.ListByJob(ctx, jobID)
	if err != nil {
		return 0
	}
	var max float64
	for _, r := range recs {
		if r.Score > max {
			max = r.Score
		}
	}
	return max
}

// reflectionLoops reads the job's typed reflection counter (0 when unset).
func (e *Engine) reflectionLoops(ctx context.Context, jobID string) int {
	return e.state.ReflectCount(ctx, jobID)
}

// jobTokenCost reads a job's recorded strategy token cost (0 when absent or the
// outcome has not been captured).
func (e *Engine) jobTokenCost(ctx context.Context, jobID string) int {
	if e.db == nil || jobID == "" {
		return 0
	}
	var cost int
	if err := e.db.DB().QueryRowContext(ctx,
		`SELECT token_cost FROM strategy_outcomes WHERE job_id = ?`, jobID).Scan(&cost); err != nil {
		return 0
	}
	return cost
}

// taskRetries is the number of earlier jobs for the task, i.e. the attempt
// index minus one. The scheduler's retry window is per task, so this is the
// retry evidence the strategy analysis needs.
func (e *Engine) taskRetries(ctx context.Context, j job.Job) int {
	if e.jobs == nil || j.TaskID == "" {
		return 0
	}
	jobs, err := e.jobs.ByTask(ctx, j.TaskID)
	if err != nil {
		return 0
	}
	for i, candidate := range jobs {
		if candidate.ID == j.ID {
			return i
		}
	}
	return 0
}

// firstCycleCandidates reads the first divergence cycle's actual candidate
// count from the audit stream. The profile records the planned count; the
// one-shot-first policy, the non-code cap, and the measured panel can change
// it before the first call.
func (e *Engine) firstCycleCandidates(ctx context.Context, jobID string) int {
	events, err := e.db.QueryEvents(ctx, store.EventFilter{JobID: jobID, Category: "jobs"})
	if err != nil {
		return 0
	}
	for _, ev := range events {
		var d struct {
			Event      string `json:"event"`
			Candidates int    `json:"candidates"`
		}
		if json.Unmarshal([]byte(ev.DataJSON), &d) != nil {
			continue
		}
		if d.Event == "divergence_start" {
			return d.Candidates
		}
	}
	return 0
}

// trajectory derives the job's executed cognitive operations from its event
// log (M8-T7; ADR-0064). Cost is attributed per phase from `llm_call` rows
// (calls + tokens); grounded verdicts come from `verification` (a
// deterministic verifier) and `tests_ran` (real tests); research and commit
// are recorded from their own audit events. The result is ordered by the
// canonical operation sequence so trajectories are comparable across jobs.
func (e *Engine) trajectory(ctx context.Context, jobID string) []cognitive.Operation {
	events, err := e.db.QueryEvents(ctx, store.EventFilter{JobID: jobID, Category: "jobs"})
	if err != nil {
		return nil
	}
	byName := map[string]*cognitive.Operation{}
	get := func(name string) *cognitive.Operation {
		if op, ok := byName[name]; ok {
			return op
		}
		op := &cognitive.Operation{Name: name}
		byName[name] = op
		return op
	}
	for _, ev := range events {
		var d struct {
			Event            string `json:"event"`
			Phase            string `json:"phase"`
			Persona          string `json:"persona"`
			PromptTokens     int    `json:"prompt_tokens"`
			CompletionTokens int    `json:"completion_tokens"`
			Applied          bool   `json:"applied"`
			Passed           bool   `json:"passed"`
			VerifierApplied  bool   `json:"verifier_applied"`
			VerifierPassed   bool   `json:"verifier_passed"`
			Outcome          string `json:"outcome"`
		}
		if json.Unmarshal([]byte(ev.DataJSON), &d) != nil {
			continue
		}
		switch d.Event {
		case "llm_call":
			name := operationForPhase(d.Phase)
			if name == "" {
				continue
			}
			op := get(name)
			op.Calls++
			op.Tokens += d.PromptTokens + d.CompletionTokens
			if op.Phase == "" {
				op.Phase = d.Phase
			}
			if op.Persona == "" {
				op.Persona = d.Persona
			}
		case "verification":
			if !d.Applied {
				continue
			}
			op := get(cognitive.OpVerify)
			op.Grounded = true
			op.Passed = d.Passed
			if op.Phase == "" {
				op.Phase = string(llm.PhaseEvaluating)
			}
		case "verification_decision":
			if !d.VerifierApplied {
				continue
			}
			op := get(cognitive.OpVerify)
			op.Grounded = true
			op.Passed = d.VerifierPassed
		case "comparison":
			get(cognitive.OpCompare)
		case "tests_ran":
			get(cognitive.OpVerify).Grounded = true
		case "research_fetch":
			if d.Outcome != "fetched" {
				continue
			}
			get(cognitive.OpResearch)
		case "git_committed":
			get(cognitive.OpCommit)
		}
	}
	out := make([]cognitive.Operation, 0, len(byName))
	for _, name := range cognitive.ExecutionOrder {
		if op, ok := byName[name]; ok {
			out = append(out, *op)
			delete(byName, name)
		}
	}
	// Defensive: emit any operation not in the canonical order (a new
	// vocabulary entry must also extend cognitive.ExecutionOrder)
	// deterministically.
	if len(byName) > 0 {
		rest := make([]string, 0, len(byName))
		for name := range byName {
			rest = append(rest, name)
		}
		sort.Strings(rest)
		for _, name := range rest {
			out = append(out, *byName[name])
		}
	}
	return out
}

// operationForPhase maps an engine phase to its cognitive operation ("" when
// the phase has no operation, e.g. an unknown phase).
func operationForPhase(phase string) string {
	switch phase {
	case llm.PhasePlanning:
		return cognitive.OpPlan
	case llm.PhaseDiverging:
		return cognitive.OpDiverge
	case llm.PhaseEvaluating:
		return cognitive.OpVerify
	case llm.PhaseRefining:
		return cognitive.OpRefine
	case llm.PhaseReflecting:
		return cognitive.OpReflect
	case llm.PhaseSynthesizing:
		return cognitive.OpSynthesize
	case llm.PhaseComparing:
		return cognitive.OpCompare
	default:
		return ""
	}
}
