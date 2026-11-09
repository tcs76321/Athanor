package firstuse

import (
	"context"
	"errors"
	"testing"

	"github.com/tcs76321/athanor/internal/architect"
	"github.com/tcs76321/athanor/internal/cycle"
	"github.com/tcs76321/athanor/internal/workflow"
)

type fakeStarter struct {
	goalID string
	jobs   []string
	err    error
}

func (f *fakeStarter) Start(_ context.Context, goalID string) ([]string, error) {
	f.goalID = goalID
	return f.jobs, f.err
}

func buildFrame(t *testing.T) architect.Frame {
	t.Helper()
	return architect.Frame{
		Name: "Ledger CLI", Goal: "Build a ledger CLI with tests.",
		Repository: t.TempDir(), Capabilities: []string{"code"},
		FirstCycle: "build", ReviewPolicy: "external actions need approval",
	}
}

func buildGraph() workflow.Graph {
	return workflow.Graph{
		Blocks: []workflow.Block{
			{ID: "implement", Kind: workflow.KindImplement},
			{ID: "test", Kind: workflow.KindTest},
		},
		Edges: []workflow.Edge{{From: "implement", To: "test"}},
	}
}

func TestApproveCycleCreatesProjectCycleAndDAG(t *testing.T) {
	idx := &fakeIndexer{sum: Summary{Indexed: 2, Chunks: 4}}
	deps, _ := newDeps(t, idx, frozen(false))
	cycles := cycle.NewRepo(deps.Events)
	starter := &fakeStarter{jobs: []string{"job-1"}}

	res, err := ApproveCycle(context.Background(), deps, cycles, starter, ApproveInput{
		Frame: buildFrame(t), Graph: buildGraph(),
	})
	if err != nil {
		t.Fatalf("ApproveCycle: %v", err)
	}
	if res.Project.ID == "" || res.Cycle.ID == "" || res.GoalID == "" {
		t.Fatalf("ids = %+v", res)
	}
	if res.Cycle.Ordinal != 0 || res.Cycle.Kind != cycle.KindBuild || res.Cycle.State != cycle.StateRunning {
		t.Errorf("cycle = %+v", res.Cycle)
	}
	if len(res.Tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(res.Tasks))
	}
	for _, task := range res.Tasks {
		if task.CycleID != res.Cycle.ID {
			t.Errorf("task %s cycle = %q, want %q", task.ID, task.CycleID, res.Cycle.ID)
		}
	}
	if starter.goalID != res.GoalID || len(res.JobIDs) != 1 || res.JobIDs[0] != "job-1" {
		t.Errorf("starter goal/jobs = %q/%v", starter.goalID, res.JobIDs)
	}
	if idx.calls != 1 || res.Index == nil || res.Index.Chunks != 4 {
		t.Errorf("index = %+v calls=%d", res.Index, idx.calls)
	}
}

func TestApproveCycleWithoutStarterLeavesApproved(t *testing.T) {
	deps, _ := newDeps(t, &fakeIndexer{}, frozen(false))
	cycles := cycle.NewRepo(deps.Events)
	res, err := ApproveCycle(context.Background(), deps, cycles, nil, ApproveInput{
		Frame: buildFrame(t), Graph: buildGraph(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Cycle.State != cycle.StateApproved {
		t.Errorf("state = %q, want approved", res.Cycle.State)
	}
	if len(res.JobIDs) != 0 {
		t.Errorf("jobs = %v", res.JobIDs)
	}
}

func TestApproveCycleRejectsInvalidGraphBeforeCreate(t *testing.T) {
	deps, _ := newDeps(t, &fakeIndexer{}, frozen(false))
	cycles := cycle.NewRepo(deps.Events)
	_, err := ApproveCycle(context.Background(), deps, cycles, nil, ApproveInput{
		Frame: buildFrame(t),
		Graph: workflow.Graph{Blocks: []workflow.Block{{ID: "a", Kind: "teleport"}}},
	})
	if err == nil {
		t.Fatal("invalid graph accepted")
	}
	var n int
	if qerr := deps.Events.DB().QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&n); qerr != nil {
		t.Fatal(qerr)
	}
	if n != 0 {
		t.Errorf("projects = %d after invalid graph, want 0", n)
	}
}

func TestApproveCycleRefusesWhenFrozen(t *testing.T) {
	deps, _ := newDeps(t, &fakeIndexer{}, frozen(true))
	cycles := cycle.NewRepo(deps.Events)
	_, err := ApproveCycle(context.Background(), deps, cycles, nil, ApproveInput{
		Frame: buildFrame(t), Graph: buildGraph(),
	})
	if !errors.Is(err, ErrFrozen) {
		t.Fatalf("err = %v, want ErrFrozen", err)
	}
}

func TestApproveCycleStarterFailureKeepsCycleApproved(t *testing.T) {
	deps, _ := newDeps(t, &fakeIndexer{}, frozen(false))
	cycles := cycle.NewRepo(deps.Events)
	starter := &fakeStarter{err: errors.New("scheduler down")}
	res, err := ApproveCycle(context.Background(), deps, cycles, starter, ApproveInput{
		Frame: buildFrame(t), Graph: buildGraph(),
	})
	if err == nil {
		t.Fatal("starter failure was swallowed")
	}
	got, gerr := cycles.Get(context.Background(), res.Cycle.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.State != cycle.StateApproved {
		t.Errorf("state = %q, want approved", got.State)
	}
}
