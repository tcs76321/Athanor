// Package decompose turns a goal into a validated, persisted task DAG
// (ROADMAP M6-T1; ARCHITECTURE §7.1; ADR-0032).
//
// It is the orchestration half of decomposition: it builds the prompt,
// makes the model call through a seam, retries a parse or validation
// failure with the simpler `main` persona, and persists the accepted graph
// through project.Repo.CreateDAG. The graph model itself — parsing and the
// deterministic validation — lives in the pure internal/dag package; no
// model is ever in the judgment path.
package decompose

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/dag"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// LLMClient is the model surface the decomposer needs. *llm.Client
// satisfies it; tests pass a scripted fake.
type LLMClient interface {
	Chat(ctx context.Context, req llm.Request) (llm.Response, error)
}

// ErrRejected reports that both decomposition attempts failed parsing or
// validation. The wrapped detail carries the last reason so the caller can
// surface a machine-readable rejection.
var ErrRejected = errors.New("dag decomposition rejected")

// ErrInfeasible reports a §12.6 context-floor violation for the attempt
// persona. Decomposition is an explicit operator action rather than a job,
// so there is no job to pause; the recommendation travels in the error.
var ErrInfeasible = errors.New("dag decomposition infeasible")

// Result is an accepted decomposition.
type Result struct {
	GoalID  string
	Tasks   []project.Task
	Persona string // the persona that produced the accepted graph
}

// Decomposer decomposes goals. It is safe for concurrent use: all state is
// per-call and the store/repo are concurrency-safe.
type Decomposer struct {
	client   LLMClient
	registry *llm.Registry
	projects *project.Repo
	events   *store.Store
	floors   config.ContextEngine
	limits   dag.Limits
	timeout  time.Duration
}

// New wires a Decomposer. floors supplies the §12.6 context floors; limits
// bounds the accepted graph; a non-positive timeout falls back to 10m.
func New(client LLMClient, registry *llm.Registry, projects *project.Repo,
	events *store.Store, floors config.ContextEngine, limits dag.Limits, timeout time.Duration) *Decomposer {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &Decomposer{
		client: client, registry: registry, projects: projects,
		events: events, floors: floors, limits: limits, timeout: timeout,
	}
}

// Decompose produces and persists a DAG for goalText under projectID.
//
// The first attempt uses `tall`; a parse or validation failure retries once
// with `main` (§7.2 "simpler strategy"). A context-floor violation is
// returned immediately as ErrInfeasible — never silently reduced. If both
// attempts fail, ErrRejected wraps the last reason and nothing is
// persisted.
func (d *Decomposer) Decompose(ctx context.Context, projectID, goalText string, criteria []string) (Result, error) {
	p, err := d.projects.Get(ctx, projectID)
	if err != nil {
		return Result{}, err
	}

	attempts := []string{llm.RoleTall, llm.RoleMain}
	var lastReason string
	for i, role := range attempts {
		persona, ok := d.registry.Persona(role)
		if !ok {
			return Result{}, fmt.Errorf("decompose: persona %q missing from registry", role)
		}
		verdict := llm.Check(persona, llm.PhasePlanning, p.Archetype, persona.ContextTarget, d.floors)
		if !verdict.Feasible {
			d.audit(ctx, map[string]any{
				"event": "dag_infeasible", "persona": role, "attempt": i + 1,
				"required": verdict.Required, "available": verdict.Available,
				"recommendation": verdict.Recommendation,
			})
			return Result{}, fmt.Errorf("%w: %s", ErrInfeasible, verdict.Recommendation)
		}

		callCtx, cancel := context.WithTimeout(ctx, d.timeout)
		resp, err := d.client.Chat(callCtx, llm.Request{
			Model:         persona.Model,
			Messages:      buildMessages(p, goalText, criteria, d.limits),
			Temperature:   llm.ResolveTemperature(llm.PhasePlanning, persona.Temperature, nil),
			ContextTarget: persona.ContextTarget,
		})
		cancel()
		if err != nil {
			// A transport failure is not a decomposition failure; the
			// retry is for model output, not connectivity.
			if errors.Is(err, llm.ErrUnreachable) {
				return Result{}, fmt.Errorf("decompose: %w", err)
			}
			return Result{}, fmt.Errorf("decompose: %s call: %w", role, err)
		}
		d.audit(ctx, map[string]any{
			"event": "dag_decomposition_call", "persona": role, "attempt": i + 1,
			"prompt_tokens": resp.PromptTokens, "completion_tokens": resp.CompletionTokens,
		})

		graph, perr := dag.Parse(resp.Content)
		if perr == nil {
			if _, verr := dag.Validate(graph, d.limits); verr != nil {
				perr = verr
			}
		}
		if perr != nil {
			lastReason = perr.Error()
			d.audit(ctx, map[string]any{
				"event": "dag_attempt_invalid", "persona": role, "attempt": i + 1, "reason": lastReason,
			})
			continue
		}

		goalID, tasks, err := d.projects.CreateDAG(ctx, projectID, goalText, criteria, toSpecs(graph))
		if err != nil {
			return Result{}, err
		}
		d.audit(ctx, map[string]any{
			"event": "dag_decomposed", "goal_id": goalID, "persona": role, "attempt": i + 1,
			"tasks": len(tasks),
		})
		return Result{GoalID: goalID, Tasks: tasks, Persona: role}, nil
	}

	d.audit(ctx, map[string]any{"event": "dag_rejected", "reason": lastReason})
	return Result{}, fmt.Errorf("%w: %s", ErrRejected, lastReason)
}

// toSpecs maps the pure graph to persistence specs. Priority is left at the
// declaration default; the scheduler (M6-T2) reads dependencies, not order.
func toSpecs(g dag.Graph) []project.TaskSpec {
	specs := make([]project.TaskSpec, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		specs = append(specs, project.TaskSpec{
			Key:         n.Key,
			ParentKey:   n.ParentKey,
			Title:       n.Title,
			Description: n.Description,
			DependsOn:   n.DependsOn,
			Criteria:    n.Criteria,
			Budget:      n.Budget,
		})
	}
	return specs
}

// buildMessages assembles the decomposition prompt. It is deliberately
// small and self-contained: decomposition works from the goal, archetype,
// criteria, and budgets, not from source chunks (ADR-0002 exempts `tall`
// from the coding floor during planning for exactly this reason).
func buildMessages(p project.Project, goal string, criteria []string, limits dag.Limits) []llm.Message {
	var b strings.Builder
	b.WriteString("Decompose the goal into a dependency-ordered DAG of implementation tasks.\n")
	fmt.Fprintf(&b, "Project archetype: %s\nProject goal: %s\n", p.Archetype, p.Goal)
	fmt.Fprintf(&b, "Goal to decompose: %s\n", goal)
	b.WriteString("Acceptance criteria:\n")
	if len(criteria) == 0 {
		b.WriteString("- (none provided)\n")
	}
	for _, c := range criteria {
		fmt.Fprintf(&b, "- %s\n", c)
	}
	b.WriteString("\nRules:\n")
	b.WriteString("- Every task is a leaf that produces a verifiable result; use `parent` to group tasks into phases.\n")
	b.WriteString("- Every leaf task must declare at least one acceptance_criteria.\n")
	b.WriteString("- `depends_on` names keys of tasks that must complete first.\n")
	b.WriteString("- Keep the graph acyclic; at least one task must have no dependencies.\n")
	fmt.Fprintf(&b, "- At most %d tasks, dependency depth at most %d, and the summed leaf max_jobs budget at most %d.\n",
		limits.MaxTasks, limits.MaxDepth, limits.MaxTotalJobs)
	b.WriteString("\nReply with a single JSON object and no other text:\n")
	b.WriteString(`{"tasks":[{"key":"string","parent":"string","title":"string","description":"string",` +
		`"depends_on":["key"],"acceptance_criteria":["string"],` +
		`"budget":{"max_jobs":1,"max_llm_calls":6,"max_tokens":20000,"max_wall_time":"30m"}}]}`)
	return []llm.Message{
		{Role: "system", Content: "You are Athanor's planning persona. You output only the requested JSON."},
		{Role: "user", Content: b.String()},
	}
}

// audit appends a `jobs` event, nil-safe (some tests do not need a store).
func (d *Decomposer) audit(ctx context.Context, data map[string]any) {
	if d.events == nil {
		return
	}
	if _, err := d.events.AppendEvent(ctx, store.Event{Category: "jobs", Data: data}); err != nil {
		slog.Error("decompose: appending event", "event", data["event"], "err", err)
	}
}
