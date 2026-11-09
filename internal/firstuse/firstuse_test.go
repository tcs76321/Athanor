package firstuse

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

type fakeEnqueuer struct{ ids []string }

func (f *fakeEnqueuer) Enqueue(id string) { f.ids = append(f.ids, id) }

type fakeIndexer struct {
	calls int
	sum   Summary
	err   error
}

func (f *fakeIndexer) IndexOnceSummary(context.Context, string, string) (Summary, error) {
	f.calls++
	return f.sum, f.err
}

type frozen bool

func (f frozen) Frozen() bool { return bool(f) }

func newDeps(t *testing.T, idx Indexer, freezer Freezer) (Deps, *fakeEnqueuer) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	eng := &fakeEnqueuer{}
	return Deps{
		Projects: project.NewRepo(st), Jobs: job.NewRepository(st),
		Engine: eng, Indexer: idx, Events: st, Freezer: freezer,
	}, eng
}

func TestCreateRunsPreindexThenStartsTheJob(t *testing.T) {
	idx := &fakeIndexer{sum: Summary{Indexed: 2, Chunks: 5, Summarized: 2}}
	deps, eng := newDeps(t, idx, frozen(false))
	res, err := Create(context.Background(), deps, Input{
		Name: "demo", Archetype: "code", Goal: "Write a small parser.",
		RepositoryPath: t.TempDir(), Start: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Project.ID == "" || res.Task.ID == "" || res.JobID == "" {
		t.Fatalf("result = %+v, want project/task/job", res)
	}
	if idx.calls != 1 || res.Index == nil || res.Index.Chunks != 5 {
		t.Errorf("pre-index = %d / %+v, want one pass with chunks=5", idx.calls, res.Index)
	}
	if len(eng.ids) != 1 || eng.ids[0] != res.JobID {
		t.Errorf("enqueued = %v, want %s", eng.ids, res.JobID)
	}
	task, err := deps.Projects.Task(context.Background(), res.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != project.TaskRunning {
		t.Errorf("task status = %q, want %q: a started job must not leave the task pending", task.Status, project.TaskRunning)
	}
}

// TestCreateInfersCapabilities proves the no-archetype product path: the
// project's capability set and first task kind come from the goal, not a form
// field (ADR-0093).
func TestCreateInfersCapabilities(t *testing.T) {
	deps, _ := newDeps(t, nil, frozen(false))
	res, err := Create(context.Background(), deps, Input{
		Name: "inferred", Goal: "Implement a small parser and write the README documentation.",
		Start: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(res.Project.Capabilities, project.CapabilityCode) ||
		!slices.Contains(res.Project.Capabilities, project.CapabilityDocument) {
		t.Errorf("capabilities = %v, want code+document", res.Project.Capabilities)
	}
	if res.Task.ID != "" {
		t.Errorf("guided create made placeholder task %q; the plan owns the first task", res.Task.ID)
	}
}

func TestCreateMetadataOnlySkipsIndexAndJob(t *testing.T) {
	idx := &fakeIndexer{}
	deps, eng := newDeps(t, idx, frozen(false))
	res, err := Create(context.Background(), deps, Input{
		Name: "meta", Archetype: "text", Goal: "Write a short note about this project.",
		RepositoryPath: t.TempDir(), Start: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.JobID != "" || len(eng.ids) != 0 || idx.calls != 0 {
		t.Errorf("metadata create started work: job=%q enq=%v index=%d", res.JobID, eng.ids, idx.calls)
	}
}

func TestCreateRefusesWhenFrozen(t *testing.T) {
	deps, _ := newDeps(t, nil, frozen(true))
	_, err := Create(context.Background(), deps, Input{Name: "frozen", Archetype: "text", Goal: "Do work.", Start: true})
	if !errors.Is(err, ErrFrozen) {
		t.Fatalf("err = %v, want ErrFrozen", err)
	}
}

func TestGuidedCreateHasNoPlaceholderTask(t *testing.T) {
	deps, eng := newDeps(t, nil, frozen(false))
	res, err := Create(context.Background(), deps, Input{
		Name: "guided", Goal: "Write a short project brief.", Start: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.ID != "" || res.JobID != "" || len(eng.ids) != 0 {
		t.Fatalf("guided create made work: task=%q job=%q enqueued=%v", res.Task.ID, res.JobID, eng.ids)
	}
	tasks, err := deps.Projects.TasksByProject(context.Background(), res.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("guided create left %d placeholder tasks, want 0", len(tasks))
	}
}

func TestDirectCreateStartsOneJob(t *testing.T) {
	deps, eng := newDeps(t, nil, frozen(false))
	res, err := Create(context.Background(), deps, Input{
		Name: "direct", Goal: "Write a short project brief.", Start: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.ID == "" || res.JobID == "" || len(eng.ids) != 1 || eng.ids[0] != res.JobID {
		t.Fatalf("direct create = task %q job %q enqueued %v, want one job", res.Task.ID, res.JobID, eng.ids)
	}
}

func TestCreateDegradesOnIndexFailure(t *testing.T) {
	idx := &fakeIndexer{err: errors.New("index unavailable")}
	deps, eng := newDeps(t, idx, frozen(false))
	res, err := Create(context.Background(), deps, Input{
		Name: "degraded", Archetype: "code", Goal: "Write a small parser for this format.",
		RepositoryPath: t.TempDir(), Start: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IndexWarning == "" || res.JobID == "" || len(eng.ids) != 1 {
		t.Errorf("degraded create = %+v / enq %v, want a warning and a started job", res, eng.ids)
	}
}

func TestCreateRejectsMissingRepositoryPath(t *testing.T) {
	deps, _ := newDeps(t, nil, frozen(false))
	if _, err := Create(context.Background(), deps, Input{
		Name: "missing", Archetype: "code", Goal: "Write a small parser.",
		RepositoryPath: filepath.Join(t.TempDir(), "does-not-exist"),
	}); err == nil {
		t.Fatal("a nonexistent repository path was accepted")
	}
}
