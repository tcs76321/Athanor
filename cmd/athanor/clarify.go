package main

import (
	"context"

	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/notebook"
)

// hitlClarifier parks a job on a model-initiated question (ADR-0112). The
// answer is carried by the HITL decision note.
type hitlClarifier struct {
	svc *hitl.Service
}

func (c hitlClarifier) Ask(ctx context.Context, jobID, projectID, phase, question string) error {
	_, err := c.svc.Await(ctx, jobID, hitl.TypeClarification, hitl.SeverityMedium,
		question, map[string]any{"phase": phase, "project_id": projectID}, 0)
	return err
}

// clarificationAnswer writes the operator's answer into the notebook before
// the HITL service resumes the job (SetPreApprover). Without this ordering the
// engine could re-read the prompt before the answer landed.
type clarificationAnswer struct {
	nb *notebook.Store
}

func (a clarificationAnswer) Approve(ctx context.Context, req hitl.Request) error {
	if a.nb == nil || req.DecisionNote == "" {
		return nil
	}
	_, err := a.nb.Append(ctx, notebook.Entry{
		JobID: req.JobID, ProjectID: req.ProjectID,
		Kind: notebook.KindAnswer, Phase: "clarification", Content: req.DecisionNote,
	})
	return err
}
