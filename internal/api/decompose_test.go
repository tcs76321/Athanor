package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/decompose"
	"github.com/tcs76321/athanor/internal/project"
)

type fakeDecomposer struct {
	res   decompose.Result
	err   error
	calls int
}

func (f *fakeDecomposer) Decompose(_ context.Context, _, _ string, _ []string) (decompose.Result, error) {
	f.calls++
	return f.res, f.err
}

func createTestProject(t *testing.T, h *harness) project.Project {
	t.Helper()
	p, _, err := h.projects.Create(context.Background(), "demo", project.ArchetypeCode,
		"Build a small REST endpoint with tests and documentation.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func postDecompose(t *testing.T, h *harness, projectID string) *http.Response {
	t.Helper()
	resp, err := http.Post(h.ts.URL+"/projects/"+projectID+"/decompose", "application/json",
		strings.NewReader(`{"goal":"Build a small REST endpoint with tests."}`))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestDecomposeRoute(t *testing.T) {
	h := newHarness(t)
	p := createTestProject(t, h)
	fake := &fakeDecomposer{res: decompose.Result{
		GoalID: "g1", Persona: "tall",
		Tasks: []project.Task{
			{ID: "t1", Title: "Design", Status: "pending", Criteria: []string{"design exists"}},
			{ID: "t2", Title: "Implement", Status: "pending", DependsOn: []string{"t1"}, Criteria: []string{"tests pass"}},
		},
	}}
	h.api.SetDecomposer(fake)

	resp := postDecompose(t, h, p.ID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var body decompositionResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.GoalID != "g1" || body.Persona != "tall" || len(body.Tasks) != 2 {
		t.Fatalf("body = %+v", body)
	}
	if len(body.Tasks[1].DependsOn) != 1 || body.Tasks[1].DependsOn[0] != "t1" {
		t.Errorf("deps = %v", body.Tasks[1].DependsOn)
	}
	if fake.calls != 1 {
		t.Errorf("calls = %d, want 1", fake.calls)
	}
}

func TestDecomposeRouteErrors(t *testing.T) {
	h := newHarness(t)
	p := createTestProject(t, h)

	// 503: no decomposer wired.
	resp := postDecompose(t, h, p.ID)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unwired status = %d, want 503", resp.StatusCode)
	}

	// 422: the model produced no valid graph.
	h.api.SetDecomposer(&fakeDecomposer{err: fmt.Errorf("%w: coverage", decompose.ErrRejected)})
	resp = postDecompose(t, h, p.ID)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("rejected status = %d, want 422", resp.StatusCode)
	}

	// 404: unknown project.
	h.api.SetDecomposer(&fakeDecomposer{err: fmt.Errorf("%w: nope", project.ErrNotFound)})
	resp = postDecompose(t, h, p.ID)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("not-found status = %d, want 404", resp.StatusCode)
	}

	// 409: frozen daemon rejects new work before the decomposer runs.
	if err := h.freezer.Freeze(context.Background()); err != nil {
		t.Fatal(err)
	}
	frozen := &fakeDecomposer{}
	h.api.SetDecomposer(frozen)
	resp = postDecompose(t, h, p.ID)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("frozen status = %d, want 409", resp.StatusCode)
	}
	if frozen.calls != 0 {
		t.Errorf("frozen daemon ran the decomposer")
	}
}

func TestProjectTasksRoute(t *testing.T) {
	h := newHarness(t)
	p := createTestProject(t, h)
	_, _, err := h.projects.CreateDAG(context.Background(), p.ID,
		"Build a small REST endpoint with tests and documentation.", nil,
		[]project.TaskSpec{
			{Key: "design", Title: "Design", Criteria: []string{"design exists"}},
			{Key: "impl", Title: "Implement", DependsOn: []string{"design"}, Criteria: []string{"tests pass"}},
		})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(h.ts.URL + "/projects/" + p.ID + "/tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Tasks []decompositionTask `json:"tasks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	// The project's create task plus the two DAG tasks.
	if len(body.Tasks) != 3 {
		t.Fatalf("tasks = %d, want 3", len(body.Tasks))
	}

	resp2, err := http.Get(h.ts.URL + "/projects/nope/tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown project status = %d, want 404", resp2.StatusCode)
	}
}

type fakeScheduler struct {
	started []string
	err     error
	calls   int
}

func (f *fakeScheduler) Start(_ context.Context, _ string) ([]string, error) {
	f.calls++
	return f.started, f.err
}

func TestGoalSubmitDecomposesAndSchedules(t *testing.T) {
	h := newHarness(t)
	p := createTestProject(t, h)
	dec := &fakeDecomposer{res: decompose.Result{
		GoalID: "g1", Persona: "tall",
		Tasks: []project.Task{{ID: "t1", Title: "A"}, {ID: "t2", Title: "B"}},
	}}
	sched := &fakeScheduler{started: []string{"j1"}}
	h.api.SetDecomposer(dec)
	h.api.SetScheduler(sched)
	h.api.SetDAGScheduling(true)

	resp, err := http.Post(h.ts.URL+"/projects/"+p.ID+"/goals", "application/json",
		strings.NewReader(`{"goal":"Build a small REST endpoint with tests."}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var body goalResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.GoalID != "g1" || len(body.JobIDs) != 1 || body.JobIDs[0] != "j1" || len(body.TaskIDs) != 2 {
		t.Fatalf("body = %+v", body)
	}
	if sched.calls != 1 {
		t.Errorf("scheduler calls = %d, want 1", sched.calls)
	}
}

func TestGoalSubmitDAGSchedulingDisabled(t *testing.T) {
	h := newHarness(t)
	p, _, err := h.projects.Create(context.Background(), "demo-text", project.ArchetypeText,
		"Write a short essay about local-first software.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	dec := &fakeDecomposer{}
	sched := &fakeScheduler{}
	h.api.SetDecomposer(dec)
	h.api.SetScheduler(sched)
	// dag_decomposition defaults off: the M1 single-task path runs.

	resp, err := http.Post(h.ts.URL+"/projects/"+p.ID+"/goals", "application/json",
		strings.NewReader(`{"goal":"Write a short essay about local-first software."}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var body goalResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.GoalID != "" || body.JobID == "" {
		t.Fatalf("body = %+v, want the single-task shape", body)
	}
	if dec.calls != 0 || sched.calls != 0 {
		t.Errorf("decomposer/scheduler ran with dag_decomposition disabled (%d/%d)", dec.calls, sched.calls)
	}
	if j := h.waitTerminal(t, body.JobID); j.State != "completed" {
		t.Errorf("job = %s, want completed", j.State)
	}
}
