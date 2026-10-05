package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/store"
)

// TestFeedbackLoopE2E is the §31.4 feedback-loop proof (ROADMAP M6-T9): a
// structured rejection creates a CorrectionRecord, and that record's derived
// rule is injected into the next job's prompt for the same project.
func TestFeedbackLoopE2E(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	corrRepo := corrections.NewRepo(e.db)
	e.eng.SetCorrectionSource(corrRepo)

	p, task, err := e.projects.Create(ctx, "feedback-loop", "text",
		"Write a short essay about local-first software.", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	// First job: produces an artifact with no corrections in scope yet.
	job1, err := e.jobs.Create(ctx, task.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.eng.Run(ctx, job1.ID)
	if strings.Contains(e.ollama.lastPrompt, "NEVER use global state") {
		t.Fatal("correction leaked into the first job's prompt")
	}
	final, err := e.artifacts.LatestForJob(ctx, job1.ID, artifact.KindDocument)
	if err != nil {
		t.Fatalf("first job produced no document artifact: %v", err)
	}

	// The user rejects the artifact with the §18.4 structured form.
	rec, err := corrRepo.Capture(ctx, corrections.CaptureInput{
		Source: corrections.SourceUserRejection, ProjectID: p.ID, ArtifactID: final.ID,
		Category: corrections.CategoryStyle, Severity: corrections.SeverityHigh,
		UserFeedback: "the essay hedged too much", DerivedRule: "NEVER use global state",
		Scope: corrections.ScopeProject,
	})
	if err != nil {
		t.Fatalf("capture rejection: %v", err)
	}

	// Second job in the same project must now see the correction.
	job2, err := e.jobs.Create(ctx, task.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.eng.Run(ctx, job2.ID)
	if !strings.Contains(e.ollama.lastPrompt, "NEVER use global state") {
		t.Fatalf("correction's derived rule missing from the second job's prompt")
	}
	if !strings.Contains(e.ollama.lastPrompt, "RELEVANT CORRECTIONS") {
		t.Fatalf("corrections section missing from the second job's prompt")
	}

	// The injection is audited for the second job and references the record.
	events, err := e.db.QueryEvents(ctx, store.EventFilter{JobID: job2.ID, Category: "feedback"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if strings.Contains(ev.DataJSON, "corrections_injected") && strings.Contains(ev.DataJSON, rec.ID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no corrections_injected audit row naming %s: %v", rec.ID, events)
	}
}
