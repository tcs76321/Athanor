package engine

import (
	"context"
	"strings"
	"testing"
)

type fakeMemoryWriter struct {
	calls int
	job   string
	text  string
}

func (f *fakeMemoryWriter) RememberJobSummary(_ context.Context, _ string, jobID, summary string) (bool, error) {
	f.calls++
	f.job = jobID
	f.text = summary
	return true, nil
}

func TestCompletedJobWritesMemorySummary(t *testing.T) {
	e := newEnv(t)
	w := &fakeMemoryWriter{}
	e.eng.SetMemoryWriter(w)
	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)
	if w.calls != 1 {
		t.Fatalf("memory writes = %d, want 1", w.calls)
	}
	if w.job != jobID {
		t.Fatalf("memory job = %q, want %q", w.job, jobID)
	}
	if !strings.Contains(w.text, "Project:") || !strings.Contains(w.text, "Task:") {
		t.Fatalf("summary = %q", w.text)
	}
}
