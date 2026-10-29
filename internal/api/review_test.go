package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
)

// TestArtifactReviewRoundTrip pins the F4 HTTP slice: an accepted artifact can
// be reviewed with provenance, listed, and disappears from the blinded queue.
func TestArtifactReviewRoundTrip(t *testing.T) {
	h := newHarness(t)
	projectID, _ := h.createProject(t)
	jobID := h.submitGoal(t, projectID)
	h.waitTerminal(t, jobID)

	arts, err := h.artifacts.ListByProject(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	var accepted artifact.Artifact
	for _, a := range arts {
		if a.Status == artifact.StatusAccepted {
			accepted = a
			break
		}
	}
	if accepted.ID == "" {
		t.Fatal("no accepted artifact to review")
	}

	resp, err := http.Post(h.ts.URL+"/artifacts/"+accepted.ID+"/review", "application/json",
		strings.NewReader(`{"score":0.8,"accepted":true,"note":"clear"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("review status = %d, want 201", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp, err = http.Get(h.ts.URL + "/artifacts/" + accepted.ID + "/reviews")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Reviews []struct {
			ID    string  `json:"id"`
			Score float64 `json:"score"`
			Note  string  `json:"note"`
		} `json:"reviews"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(list.Reviews) != 1 || list.Reviews[0].Score != 0.8 || list.Reviews[0].Note != "clear" {
		t.Fatalf("reviews = %+v", list.Reviews)
	}

	resp, err = http.Get(h.ts.URL + "/reviews/queue")
	if err != nil {
		t.Fatal(err)
	}
	var queue struct {
		Queue []struct {
			ID string `json:"id"`
		} `json:"queue"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&queue); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	for _, q := range queue.Queue {
		if q.ID == accepted.ID {
			t.Error("reviewed artifact still in the queue")
		}
	}
}
