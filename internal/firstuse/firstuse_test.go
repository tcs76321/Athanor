package firstuse

import (
	"context"
	"errors"
	"path/filepath"
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
		RepositoryPath: "/tmp/repo", Start: true,
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
}

func TestCreateMetadataOnlySkipsIndexAndJob(t *testing.T) {
	idx := &fakeIndexer{}
	deps, eng := newDeps(t, idx, frozen(false))
	res, err := Create(context.Background(), deps, Input{
		Name: "meta", Archetype: "text", Goal: "Write a short note about this project.",
		RepositoryPath: "/tmp/repo", Start: false,
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

func TestCreateDegradesOnIndexFailure(t *testing.T) {
	idx := &fakeIndexer{err: errors.New("index unavailable")}
	deps, eng := newDeps(t, idx, frozen(false))
	res, err := Create(context.Background(), deps, Input{
		Name: "degraded", Archetype: "code", Goal: "Write a small parser for this format.",
		RepositoryPath: "/tmp/repo", Start: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IndexWarning == "" || res.JobID == "" || len(eng.ids) != 1 {
		t.Errorf("degraded create = %+v / enq %v, want a warning and a started job", res, eng.ids)
	}
}
