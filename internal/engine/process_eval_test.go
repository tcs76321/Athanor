package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
)

func TestRunWritesProcessEvaluation(t *testing.T) {
	e := newEnv(t)
	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)

	events, err := e.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if strings.Contains(ev.DataJSON, `"event":"process_eval"`) {
			found = true
			if !strings.Contains(ev.DataJSON, `"verdict"`) {
				t.Fatalf("process_eval has no verdict: %s", ev.DataJSON)
			}
		}
	}
	if !found {
		t.Fatal("no process_eval event for the completed job")
	}
}
