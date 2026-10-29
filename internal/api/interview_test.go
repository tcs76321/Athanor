package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/interview"
)

// TestInterviewPlanApprovalStartsWork is the F3 loop over HTTP: metadata-only
// create, resumable answers, a generated plan, approval, then one job.
func TestInterviewPlanApprovalStartsWork(t *testing.T) {
	h := newHarness(t)
	projectID, _ := h.createProject(t)

	resp, err := http.Get(h.ts.URL + "/projects/" + projectID + "/interview")
	if err != nil {
		t.Fatal(err)
	}
	var fresh interview.Interview
	if err := json.NewDecoder(resp.Body).Decode(&fresh); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if fresh.Status != interview.StatusQuestions || len(fresh.Questions) == 0 {
		t.Fatalf("fresh interview = %+v", fresh)
	}

	resp, err = http.Post(h.ts.URL+"/projects/"+projectID+"/interview", "application/json",
		strings.NewReader(`{"answers":{"audience":"developers evaluating local-first tools"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var planned interview.Interview
	if err := json.NewDecoder(resp.Body).Decode(&planned); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if planned.Status != interview.StatusPlanReady || !strings.Contains(planned.Plan, "developers evaluating") {
		t.Fatalf("plan = %+v", planned)
	}

	resp, err = http.Post(h.ts.URL+"/projects/"+projectID+"/interview/approve", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var decision struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decision); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if decision.JobID == "" {
		t.Fatal("approval did not start a job")
	}
	if j := h.waitTerminal(t, decision.JobID); j.State != "completed" {
		t.Errorf("job state = %s, want completed", j.State)
	}
}
