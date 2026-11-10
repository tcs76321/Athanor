package main

import (
	"context"
	"fmt"

	"github.com/tcs76321/athanor/internal/cycle"
	"github.com/tcs76321/athanor/internal/firstuse"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/trigger"
	"github.com/tcs76321/athanor/internal/workflow"
)

// templateLauncher starts a scheduled cycle from the built-in template the
// trigger names. It is the only bridge between the trigger runner and the
// scheduler; the runner never compiles or executes anything itself.
type templateLauncher struct {
	projects *project.Repo
	cycles   *cycle.Repo
	deps     firstuse.Deps
	starter  firstuse.Starter
}

func (l templateLauncher) Launch(ctx context.Context, tr trigger.Record) (string, error) {
	p, err := l.projects.Get(ctx, tr.ProjectID)
	if err != nil {
		return "", fmt.Errorf("trigger: loading project %s: %w", tr.ProjectID, err)
	}
	tpl, ok := workflow.TemplateByID(tr.TemplateID)
	if !ok {
		return "", fmt.Errorf("trigger: unknown template %q", tr.TemplateID)
	}
	res, err := firstuse.StartTemplate(ctx, l.deps, l.cycles, l.starter, p, tpl, tr.ID)
	if err != nil {
		return "", err
	}
	return res.Cycle.ID, nil
}
