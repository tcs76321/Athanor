package firstuse

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/architect"
	"github.com/tcs76321/athanor/internal/cycle"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/source"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/workflow"
)

// Starter starts the ready leaves of a goal's DAG. *scheduler.Scheduler
// satisfies it; a nil starter leaves the cycle approved but unstarted.
type Starter interface {
	Start(ctx context.Context, goalID string) ([]string, error)
}

// ApproveInput is the reviewed frame and graph. The caller is responsible for
// one approval per proposal; this function does not deduplicate sessions.
type ApproveInput struct {
	Frame    architect.Frame
	Graph    workflow.Graph
	Priority string
}

// ApproveResult is the created project, cycle, compiled DAG, and started jobs.
type ApproveResult struct {
	Project      project.Project
	Cycle        cycle.Cycle
	GoalID       string
	Tasks        []project.Task
	JobIDs       []string
	Index        *Summary
	IndexWarning string
}

// CreateDraft turns a reviewed frame and graph into a project and a draft
// cycle. The canvas edits the draft; StartDraft compiles and starts it.
func CreateDraft(ctx context.Context, deps Deps, cycles *cycle.Repo, in ApproveInput) (ApproveResult, error) {
	if cycles == nil {
		return ApproveResult{}, fmt.Errorf("firstuse: cycle repository is required")
	}
	if deps.Freezer != nil && deps.Freezer.Frozen() {
		return ApproveResult{}, ErrFrozen
	}
	graph, err := workflow.Normalize(in.Graph)
	if err != nil {
		return ApproveResult{}, err
	}
	kind, err := cycleKind(in.Frame.FirstCycle)
	if err != nil {
		return ApproveResult{}, err
	}
	if strings.TrimSpace(in.Frame.Name) == "" || strings.TrimSpace(in.Frame.Goal) == "" {
		return ApproveResult{}, fmt.Errorf("firstuse: frame name and goal are required")
	}

	created, err := Create(ctx, deps, Input{
		Name:           in.Frame.Name,
		Goal:           in.Frame.Goal,
		RepositoryPath: in.Frame.Repository,
		Boundaries:     strings.Join(in.Frame.Constraints, "; "),
		Sources:        append([]string(nil), in.Frame.Sources...),
		Priority:       in.Priority,
		Start:          false, // the cycle's compiled DAG starts the work
	})
	if err != nil {
		return ApproveResult{}, err
	}
	p := created.Project
	if len(in.Frame.Capabilities) > 0 {
		if err := deps.Projects.UpdateCapabilities(ctx, p.ID, in.Frame.Capabilities); err != nil {
			return ApproveResult{}, fmt.Errorf("firstuse: setting frame capabilities: %w", err)
		}
		if got, err := deps.Projects.Get(ctx, p.ID); err == nil {
			p = got
		}
	}
	res := ApproveResult{Project: p, Index: created.Index, IndexWarning: created.IndexWarning}
	if p.RepositoryPath != "" && deps.Indexer != nil {
		ictx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		sum, ierr := deps.Indexer.IndexOnceSummary(ictx, p.ID, p.RepositoryPath)
		cancel()
		if ierr != nil {
			res.IndexWarning = "repository pre-index failed: " + ierr.Error()
			slog.Warn("firstuse: cycle pre-index failed", "project", p.ID, "err", ierr)
		} else {
			res.Index = &sum
		}
	}

	c, err := cycles.Create(ctx, p.ID, kind, in.Frame.Goal)
	if err != nil {
		return ApproveResult{}, fmt.Errorf("firstuse: creating cycle: %w", err)
	}
	frameJSON, err := json.Marshal(in.Frame)
	if err != nil {
		return ApproveResult{}, fmt.Errorf("firstuse: encoding frame: %w", err)
	}
	if err := cycles.SetFrame(ctx, c.ID, string(frameJSON), in.Frame.ReviewPolicy); err != nil {
		return ApproveResult{}, err
	}
	graphJSON, err := json.Marshal(graph)
	if err != nil {
		return ApproveResult{}, fmt.Errorf("firstuse: encoding graph: %w", err)
	}
	if err := cycles.SetGraph(ctx, c.ID, string(graphJSON)); err != nil {
		return ApproveResult{}, err
	}
	res.Cycle = c
	if deps.Events != nil {
		_, _ = deps.Events.AppendEvent(ctx, store.Event{
			Category: "projects", ProjectID: p.ID,
			Data: map[string]any{
				"event": "cycle_drafted", "cycle": c.ID, "kind": string(kind),
				"blocks": len(graph.Blocks),
			},
		})
	}
	return res, nil
}

// StartDraft compiles the draft graph and starts its ready leaves. The
// existing scheduler is the only executor; this function does not run work.
func StartDraft(ctx context.Context, deps Deps, cycles *cycle.Repo, starter Starter, cycleID string) (ApproveResult, error) {
	if cycles == nil {
		return ApproveResult{}, fmt.Errorf("firstuse: cycle repository is required")
	}
	c, err := cycles.Get(ctx, cycleID)
	if err != nil {
		return ApproveResult{}, err
	}
	if c.State == cycle.StateRunning {
		return ApproveResult{}, fmt.Errorf("firstuse: cycle %s is already running", cycleID)
	}
	if c.State == cycle.StateCompleted || c.State == cycle.StateFailed || c.State == cycle.StateCancelled {
		return ApproveResult{}, fmt.Errorf("firstuse: cycle %s is terminal", cycleID)
	}
	p, err := deps.Projects.Get(ctx, c.ProjectID)
	if err != nil {
		return ApproveResult{}, err
	}
	var graph workflow.Graph
	if err := json.Unmarshal([]byte(c.GraphJSON), &graph); err != nil {
		return ApproveResult{}, fmt.Errorf("firstuse: decoding cycle graph: %w", err)
	}
	if err := workflow.Validate(graph, workflow.DefaultLimits()); err != nil {
		return ApproveResult{}, err
	}
	graph = injectCoverage(ctx, deps.Coverage, p.ID, graph)
	manifest, err := source.LoadLocal(p.RepositoryPath,
		source.ProjectSpecs(p.Sources, []string{"wire"}), source.DefaultLimits())
	if err != nil {
		return ApproveResult{}, fmt.Errorf("firstuse: loading project sources: %w", err)
	}
	specs, err := workflow.Compile(graph, workflow.CompileOptions{
		CycleID:     c.ID,
		Sources:     p.Sources,
		SourceTexts: manifest.SourceTexts(),
	})
	if err != nil {
		return ApproveResult{}, err
	}
	goalID, tasks, err := deps.Projects.CreateDAG(ctx, p.ID, c.Goal, specs)
	if err != nil {
		return ApproveResult{}, fmt.Errorf("firstuse: creating cycle DAG: %w", err)
	}
	if err := cycles.SetState(ctx, c.ID, cycle.StateApproved); err != nil {
		return ApproveResult{}, err
	}
	res := ApproveResult{Project: p, Cycle: c, GoalID: goalID, Tasks: tasks}
	if starter != nil {
		jobs, err := starter.Start(ctx, goalID)
		if err != nil {
			return res, fmt.Errorf("firstuse: starting cycle: %w", err)
		}
		res.JobIDs = jobs
		if err := cycles.SetState(ctx, c.ID, cycle.StateRunning); err != nil {
			return res, err
		}
	}
	res.Cycle, _ = cycles.Get(ctx, c.ID)
	if deps.Events != nil {
		_, _ = deps.Events.AppendEvent(ctx, store.Event{
			Category: "projects", ProjectID: p.ID,
			Data: map[string]any{
				"event": "cycle_started", "cycle": c.ID, "kind": string(c.Kind),
				"blocks": len(graph.Blocks), "goal": goalID,
				"jobs": len(res.JobIDs), "started": starter != nil,
			},
		})
	}
	return res, nil
}

// CreateTemplateDraft creates a draft cycle from a built-in template. The
// project supplies the goal, sources, and capabilities; the template supplies
// the graph. Nothing runs until StartDraft. The trigger ID records which
// trigger (if any) will start this cycle, including manual starts.
func CreateTemplateDraft(ctx context.Context, cycles *cycle.Repo, p project.Project, tpl workflow.Template, triggerID string) (cycle.Cycle, error) {
	if cycles == nil {
		return cycle.Cycle{}, fmt.Errorf("firstuse: cycle repository is required")
	}
	if err := workflow.ValidateTemplate(tpl); err != nil {
		return cycle.Cycle{}, err
	}
	c, err := cycles.Create(ctx, p.ID, cycle.Kind(tpl.CycleKind), p.Goal)
	if err != nil {
		return cycle.Cycle{}, fmt.Errorf("firstuse: creating cycle: %w", err)
	}
	graphJSON, err := json.Marshal(tpl.Graph)
	if err != nil {
		return cycle.Cycle{}, fmt.Errorf("firstuse: encoding graph: %w", err)
	}
	if err := cycles.SetGraph(ctx, c.ID, string(graphJSON)); err != nil {
		return cycle.Cycle{}, err
	}
	if err := cycles.SetGraphRef(ctx, c.ID, tpl.ID, tpl.Version, triggerID); err != nil {
		return cycle.Cycle{}, err
	}
	frame := architect.Frame{
		Name: p.Name, Goal: p.Goal, Outcome: p.Goal,
		Repository: p.RepositoryPath, Sources: append([]string(nil), p.Sources...),
		Capabilities: append([]string(nil), p.Capabilities...),
		FirstCycle:   tpl.CycleKind,
		ReviewPolicy: "internal work may proceed; external or irreversible actions require approval",
	}
	frameJSON, err := json.Marshal(frame)
	if err != nil {
		return cycle.Cycle{}, fmt.Errorf("firstuse: encoding frame: %w", err)
	}
	if err := cycles.SetFrame(ctx, c.ID, string(frameJSON), frame.ReviewPolicy); err != nil {
		return cycle.Cycle{}, err
	}
	return cycles.Get(ctx, c.ID)
}

// StartTemplate creates a draft from the template and starts it through the
// existing scheduler. A schedule trigger uses this same path.
func StartTemplate(ctx context.Context, deps Deps, cycles *cycle.Repo, starter Starter, p project.Project, tpl workflow.Template, triggerID string) (ApproveResult, error) {
	c, err := CreateTemplateDraft(ctx, cycles, p, tpl, triggerID)
	if err != nil {
		return ApproveResult{}, err
	}
	return StartDraft(ctx, deps, cycles, starter, c.ID)
}

// ApproveCycle is the one-shot form used by the conversation approval path:
// create the draft, compile it, and start it.
func ApproveCycle(ctx context.Context, deps Deps, cycles *cycle.Repo, starter Starter, in ApproveInput) (ApproveResult, error) {
	draft, err := CreateDraft(ctx, deps, cycles, in)
	if err != nil {
		return ApproveResult{}, err
	}
	res, err := StartDraft(ctx, deps, cycles, starter, draft.Cycle.ID)
	if err != nil {
		return res, err
	}
	res.Index, res.IndexWarning = draft.Index, draft.IndexWarning
	return res, nil
}

func cycleKind(kind string) (cycle.Kind, error) {
	switch kind {
	case "discovery":
		return cycle.KindDiscovery, nil
	case "design":
		return cycle.KindDesign, nil
	case "build":
		return cycle.KindBuild, nil
	default:
		return "", fmt.Errorf("firstuse: unknown first cycle %q", kind)
	}
}
