package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/cycle"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/workflow"
	"github.com/tcs76321/athanor/migrations"
)

type canvasStarter struct {
	goal string
	jobs []string
}

func (c *canvasStarter) Start(_ context.Context, goalID string) ([]string, error) {
	c.goal = goalID
	return c.jobs, nil
}

func canvasSetup(t *testing.T) (*UI, *httptest.Server, project.Project, cycle.Cycle, *canvasStarter) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	projects := project.NewRepo(s)
	p, err := projects.CreateGuided(ctx, project.Intent{
		Name: "canvas-demo", Goal: "Build a small parser with tests.",
		RepositoryPath: t.TempDir(),
		Sources:        []string{"https://example.com/a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cycles := cycle.NewRepo(s)
	c, err := cycles.Create(ctx, p.ID, cycle.KindBuild, "Build a small parser with tests.")
	if err != nil {
		t.Fatal(err)
	}
	graph := `{"blocks":[{"id":"implement","kind":"implement"},{"id":"test","kind":"test"}],"edges":[{"from":"implement","to":"test"}]}`
	if err := cycles.SetGraph(ctx, c.ID, graph); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	starter := &canvasStarter{jobs: []string{"job-1"}}
	u := New(Deps{
		Store: s, Projects: projects, Jobs: job.NewRepository(s), Config: cfg,
		Cycles: cycles, Starter: starter, Freezer: fakeFreezer{}, Engine: &fakeEngine{},
	})
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return u, ts, p, c, starter
}

func TestCanvasGraphJSONAndPage(t *testing.T) {
	_, ts, p, c, _ := canvasSetup(t)
	code, body := get(t, ts.URL+"/ui/cycles/"+c.ID+"/graph.json")
	if code != http.StatusOK {
		t.Fatalf("graph status = %d: %s", code, body)
	}
	if !strings.Contains(body, `"registry"`) || !strings.Contains(body, `"implement"`) {
		t.Errorf("graph body = %s", body)
	}
	code, body = get(t, ts.URL+"/ui/projects/"+p.ID+"/canvas")
	if code != http.StatusOK {
		t.Fatalf("canvas status = %d", code)
	}
	if !strings.Contains(body, "canvas-shell") || !strings.Contains(body, "canvas.js") {
		t.Error("canvas page missing shell/assets")
	}
}

func TestCanvasSaveValidatesGraph(t *testing.T) {
	_, ts, _, c, _ := canvasSetup(t)
	bad := `{"blocks":[{"id":"a","kind":"teleport"}],"edges":[]}`
	resp, err := http.Post(ts.URL+"/ui/cycles/"+c.ID+"/graph", "application/json", bytes.NewBufferString(bad))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid graph status = %d, want 400", resp.StatusCode)
	}
	good := `{"blocks":[{"id":"research","kind":"research"},{"id":"fetch","kind":"fetch"}],"edges":[{"from":"research","to":"fetch"}]}`
	resp, err = http.Post(ts.URL+"/ui/cycles/"+c.ID+"/graph", "application/json", bytes.NewBufferString(good))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid graph status = %d", resp.StatusCode)
	}
	var data struct {
		Graph workflow.Graph `json:"graph"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if len(data.Graph.Blocks) != 2 {
		t.Errorf("saved graph = %+v", data.Graph)
	}
}

func TestCanvasStartCompilesAndCallsStarter(t *testing.T) {
	u, ts, p, c, starter := canvasSetup(t)
	resp, err := http.Post(ts.URL+"/ui/cycles/"+c.ID+"/start", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d", resp.StatusCode)
	}
	var data struct {
		OK    bool     `json:"ok"`
		Goal  string   `json:"goal"`
		Jobs  []string `json:"jobs"`
		State string   `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if !data.OK || data.Goal == "" || len(data.Jobs) != 1 || starter.goal != data.Goal {
		t.Errorf("start = %+v starter=%+v", data, starter)
	}
	tasks, err := u.deps.Projects.TasksByProject(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Errorf("tasks = %d, want 2", len(tasks))
	}
	for _, task := range tasks {
		if task.CycleID != c.ID {
			t.Errorf("task %s cycle = %q, want %q", task.ID, task.CycleID, c.ID)
		}
	}
}
