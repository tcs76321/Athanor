package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/workflow"
)

func TestCreateCycleFromDailyBriefTemplate(t *testing.T) {
	u, ts, p, _, starter := canvasSetup(t)

	code, body := get(t, ts.URL+"/ui/projects/"+p.ID+"/cycles/new")
	if code != http.StatusOK {
		t.Fatalf("new-cycle page = %d: %s", code, body)
	}
	if !strings.Contains(body, "Daily Brief") || !strings.Contains(body, "daily-brief") {
		t.Fatalf("templates missing from page: %s", body)
	}

	resp := postForm(t, ts.URL+"/ui/projects/"+p.ID+"/cycles", url.Values{"template_id": {"daily-brief"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/canvas?cycle=") {
		t.Fatalf("location = %q", loc)
	}
	cycleID := loc[strings.Index(loc, "cycle=")+len("cycle="):]

	code, body = get(t, ts.URL+"/ui/cycles/"+cycleID+"/graph.json")
	if code != http.StatusOK {
		t.Fatalf("graph = %d: %s", code, body)
	}
	for _, want := range []string{"daily-brief", "operate", "synthesize", "retain"} {
		if !strings.Contains(body, want) {
			t.Errorf("graph missing %q", want)
		}
	}

	resp, err := http.Post(ts.URL+"/ui/cycles/"+cycleID+"/start", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start = %d", resp.StatusCode)
	}
	if starter.goal == "" {
		t.Fatal("starter was not called")
	}
	tasks, err := u.deps.Projects.TasksByGoal(context.Background(), starter.goal)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != len(workflow.DailyBriefTemplate().Graph.Blocks) {
		t.Fatalf("tasks = %d, want %d", len(tasks), len(workflow.DailyBriefTemplate().Graph.Blocks))
	}
	var researchDesc string
	var researchCriteria []string
	foundResearch := false
	for i := range tasks {
		if tasks[i].Title == "Find today's sources" {
			foundResearch = true
			researchDesc = tasks[i].Description
			researchCriteria = tasks[i].Criteria
		}
	}
	if !foundResearch {
		t.Fatal("research task missing")
	}
	if !strings.Contains(researchDesc, "every claim names the URL") {
		t.Errorf("contract not compiled: %q", researchDesc)
	}
	if !strings.Contains(researchDesc, "https://example.com/a") {
		t.Errorf("project sources not compiled: %q", researchDesc)
	}
	if len(researchCriteria) == 0 {
		t.Error("block acceptance not compiled")
	}
}

func TestCreateCycleRejectsUnknownTemplate(t *testing.T) {
	_, ts, p, _, _ := canvasSetup(t)
	resp := postForm(t, ts.URL+"/ui/projects/"+p.ID+"/cycles", url.Values{"template_id": {"nope"}})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown template = %d, want 404", resp.StatusCode)
	}
}
