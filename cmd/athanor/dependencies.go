package main

import (
	"context"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/engine/contextasm"
	"github.com/tcs76321/athanor/internal/project"
)

// dependencyOutputs is the production read path for a block graph's data flow
// (ADR-0134 §2): a downstream task's prompt includes the accepted artifact of
// each completed upstream task. The newest accepted artifact wins; a task with
// only drafts contributes its newest draft, because a completed task's
// artifact may not have finished the review transition yet.
type dependencyOutputs struct {
	projects  *project.Repo
	artifacts *artifact.Store
}

func (d dependencyOutputs) TaskOutputs(ctx context.Context, projectID string, taskIDs []string) ([]contextasm.DependencyOutput, error) {
	out := make([]contextasm.DependencyOutput, 0, len(taskIDs))
	for _, id := range taskIDs {
		t, err := d.projects.Task(ctx, id)
		if err != nil {
			continue
		}
		arts, err := d.artifacts.ListByTask(ctx, id)
		if err != nil || len(arts) == 0 {
			continue
		}
		chosen := arts[0]
		for _, a := range arts {
			if a.Status == artifact.StatusAccepted {
				chosen = a
				break
			}
		}
		content, err := d.artifacts.ReadContent(ctx, chosen.ID)
		if err != nil || len(content) == 0 {
			continue
		}
		out = append(out, contextasm.DependencyOutput{
			TaskID: id, Title: t.Title, Content: string(content),
		})
	}
	return out, nil
}
