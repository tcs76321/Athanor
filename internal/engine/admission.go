package engine

import (
	"context"
	"errors"

	"github.com/tcs76321/athanor/internal/project"
)

// LaneAdmission is the ADR-0125 lane surface. A nil admission preserves the
// aggregate in-flight cap.
type LaneAdmission interface {
	Acquire(ctx context.Context, lane string) (release func(), err error)
	MaxConcurrentJobs() int
}

// SetAdmission wires lane-aware admission. Call before serving work.
func (e *Engine) SetAdmission(a LaneAdmission) { e.admission = a }

// laneFor classifies a job into the solo, pods, or light lane. Repo-backed
// code is solo; other code is pods; everything else is light.
func (e *Engine) laneFor(ctx context.Context, jobID string) string {
	j, err := e.jobs.Get(ctx, jobID)
	if err != nil {
		return "light"
	}
	t, err := e.projects.Task(ctx, j.TaskID)
	if err != nil {
		return "light"
	}
	p, err := e.projects.Get(ctx, t.ProjectID)
	if err != nil {
		return "light"
	}
	if capabilityFor(p, t) == project.ArchetypeCode {
		if contextDemandFor(p, t) == "repo" {
			return "solo"
		}
		return "pods"
	}
	return "light"
}

// isAdmissionDenied distinguishes a policy refusal (fall back to legacy) from
// a canceled context (abandon the wait).
func isAdmissionDenied(err error) bool {
	return err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
