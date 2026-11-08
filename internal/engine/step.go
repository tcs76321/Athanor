package engine

import (
	"context"
	"errors"

	"github.com/tcs76321/athanor/internal/engine/phases"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
)

// step performs the single phase named by j.State. The two pre-phase
// transitions stay here so lifecycle ownership is unambiguous; the seven §13.1
// phases run in the phase package.
func (e *Engine) step(ctx context.Context, j job.Job) error {
	switch j.State {
	case job.StateQueued:
		_, err := e.jobs.Transition(ctx, j.ID, job.StateContextBuilding)
		return err
	case job.StateContextBuilding:
		_, err := e.jobs.Transition(ctx, j.ID, job.StatePlanning)
		return err
	default:
		if e.phaseDeps == nil {
			return errors.New("engine: phase dependencies are not wired")
		}
		return phases.Step(ctx, e.phaseDeps, j)
	}
}

// contexts loads the project and task a job executes for. The phase package
// has its own copy through the port; the root keeps one for the terminal
// memory write and strategy capture.
func (e *Engine) contexts(ctx context.Context, j job.Job) (project.Project, project.Task, error) {
	t, err := e.projects.Task(ctx, j.TaskID)
	if err != nil {
		return project.Project{}, project.Task{}, err
	}
	p, err := e.projects.Get(ctx, t.ProjectID)
	if err != nil {
		return project.Project{}, project.Task{}, err
	}
	return p, t, nil
}
