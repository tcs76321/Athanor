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

// ApproveCycle turns a reviewed frame and graph into a project, a cycle, a
// compiled DAG, and (when a starter is present) running jobs. The existing
// scheduler is the only executor; this function does not run work itself.
func ApproveCycle(ctx context.Context, deps Deps, cycles *cycle.Repo, starter Starter, in ApproveInput) (ApproveResult, error) {
	if cycles == nil {
		return ApproveResult{}, fmt.Errorf("firstuse: cycle repository is required")
	}
	if deps.Freezer != nil && deps.Freezer.Frozen() {
		return ApproveResult{}, ErrFrozen
	}
	if err := workflow.Validate(in.Graph, workflow.DefaultLimits()); err != nil {
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
	// The guided create is metadata-only, so run the one bounded pre-index
	// here as well: the first cycle's code tasks must not start blind.
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

	specs, err := workflow.Compile(in.Graph, workflow.CompileOptions{CycleID: c.ID})
	if err != nil {
		return ApproveResult{}, err
	}
	goalID, tasks, err := deps.Projects.CreateDAG(ctx, p.ID, in.Frame.Goal, specs)
	if err != nil {
		return ApproveResult{}, fmt.Errorf("firstuse: creating cycle DAG: %w", err)
	}
	res.Cycle, res.GoalID, res.Tasks = c, goalID, tasks
	if err := cycles.SetState(ctx, c.ID, cycle.StateApproved); err != nil {
		return ApproveResult{}, err
	}

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
				"event": "cycle_created", "cycle": c.ID, "kind": string(kind),
				"blocks": len(in.Graph.Blocks), "goal": goalID,
				"jobs": len(res.JobIDs), "started": starter != nil,
			},
		})
	}
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
