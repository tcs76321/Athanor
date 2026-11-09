package intake

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/discovery"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/planning"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func TestInferOnRamp(t *testing.T) {
	if got := project.InferOnRamp("Add pagination to the users endpoint", "/repo", []string{project.CapabilityCode}); got != project.OnRampDirect {
		t.Fatalf("repo-backed code goal = %q, want direct", got)
	}
	if got := project.InferOnRamp("Help me figure out what small service to build", "", []string{project.CapabilityDocument}); got != project.OnRampExplore {
		t.Fatalf("exploratory prose goal = %q, want explore", got)
	}
	if got := project.InferOnRamp("Write a short essay about local-first software", "", []string{project.CapabilityText}); got != project.OnRampExplore {
		t.Fatalf("prose goal without a repository = %q, want explore", got)
	}
}

func TestStageDerivesEveryTransition(t *testing.T) {
	explore := project.Project{OnRamp: project.OnRampExplore}
	cases := []struct {
		name  string
		st    State
		tasks bool
		want  Stage
	}{
		{"no directions", State{Project: explore}, false, StageIntent},
		{"directions running", State{Project: explore, DirectionRun: &Run{Status: statusRunning}}, false, StageDirections},
		{"directions interrupted", State{Project: explore, HasDirections: true, Directions: discovery.Session{Status: "generating"}}, false, StageIntent},
		{"choose", State{Project: explore, HasDirections: true, Directions: discovery.Session{Status: "ready", Options: []discovery.Option{{ID: "o1"}}}}, false, StageChoose},
		{"plan draft", State{Project: explore, HasDirections: true, Directions: discovery.Session{ID: "s", Status: "ready", SelectedID: "o1", Options: []discovery.Option{{ID: "o1"}}}}, false, StagePlan},
		{"plan interrupted", State{Project: explore, HasDirections: true, Directions: discovery.Session{ID: "s", Status: "ready", SelectedID: "o1", Options: []discovery.Option{{ID: "o1"}}}, HasPlan: true, Plan: planning.Session{Status: "generating"}}, false, StagePlan},
		{"working", State{Project: explore}, true, StageWorking},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := c.st
			evaluate(&st, c.tasks)
			if st.Stage != c.want {
				t.Fatalf("stage = %q, want %q", st.Stage, c.want)
			}
		})
	}
}

func TestDirectOnRampSkipsDirections(t *testing.T) {
	st := State{Project: project.Project{OnRamp: project.OnRampDirect}}
	evaluate(&st, false)
	if st.Stage != StagePlan {
		t.Fatalf("direct stage = %q, want plan", st.Stage)
	}
}

func TestStalePlanIsIgnored(t *testing.T) {
	st := State{
		Project:       project.Project{OnRamp: project.OnRampExplore},
		HasDirections: true,
		Directions: discovery.Session{ID: "s", Status: "ready", SelectedID: "o1",
			Options: []discovery.Option{{ID: "o1"}, {ID: "o2"}}},
		HasPlan: true,
		Plan:    planning.Session{OptionID: "o2", Status: "ready", Candidates: []planning.Candidate{{ID: "c1", Status: "ready"}}},
	}
	evaluate(&st, false)
	if st.HasPlan {
		t.Fatal("a plan generated for a different direction was kept")
	}
	if st.Stage != StagePlan {
		t.Fatalf("stage = %q, want plan", st.Stage)
	}
}

func TestFilterSourcesUsesDotBoundary(t *testing.T) {
	got := FilterSources([]string{"https://example.com/a", "https://evil-example.com/b", "https://other.test/c"}, []string{"example.com"})
	if len(got) != 1 || got[0] != "https://example.com/a" {
		t.Fatalf("filtered = %v, want only the allowlisted example.com URL", got)
	}
	if all := FilterSources([]string{"https://example.com/a"}, nil); len(all) != 1 {
		t.Fatalf("nil allowlist should pass through for tests, got %v", all)
	}
	if none := FilterSources([]string{"https://example.com/a"}, []string{}); len(none) != 0 {
		t.Fatalf("empty allowlist must deny all, got %v", none)
	}
}

func TestApproveDirectDeterministicPlan(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	repo := project.NewRepo(st)
	p, err := repo.CreateGuided(context.Background(), project.Intent{
		Goal: "Build a small add function with tests.", OnRamp: project.OnRampDirect,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Deps{Projects: repo, Jobs: job.NewRepository(st), Events: st})
	res, err := svc.Approve(context.Background(), p.ID, "deterministic")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if res.PlanID != "deterministic" || len(res.Tasks) == 0 {
		t.Fatalf("result = %+v", res)
	}
	got, err := repo.Get(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ApprovedPlanID != "deterministic" {
		t.Fatalf("approved plan = %q", got.ApprovedPlanID)
	}
}

func TestBuildDeterministicPlanUsesCheckAsCriteria(t *testing.T) {
	_, specs := BuildDeterministicPlan(project.Project{
		Name: "Brief", Goal: "Write a daily brief.", Capabilities: []string{project.CapabilityDocument},
		Boundaries: "No external publishing.",
	}, discovery.Option{
		Title: "Daily brief", Outcome: "One coherent update", Method: "Group by consequence",
		Check: "Five readers review it",
	})
	if len(specs) == 0 {
		t.Fatal("no tasks")
	}
	if !strings.Contains(strings.Join(specs[1].Criteria, " "), "Five readers review it") {
		t.Fatalf("deliverable criteria do not carry the check: %v", specs[1].Criteria)
	}
}
