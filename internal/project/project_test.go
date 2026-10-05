package project

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func openRepo(t *testing.T) (*Repo, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	return NewRepo(s), s
}

const validGoal = "Write a short essay about local-first software."

func TestCreatePersistsProjectGoalTask(t *testing.T) {
	r, s := openRepo(t)
	ctx := context.Background()

	p, task, err := r.Create(ctx, "demo", ArchetypeText, validGoal, "/tmp/demo-repo", []string{"three arguments"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "demo" || p.Archetype != ArchetypeText || p.Status != "active" {
		t.Errorf("project = %+v", p)
	}
	if p.RepositoryPath != "/tmp/demo-repo" {
		t.Errorf("repository path = %q, want /tmp/demo-repo", p.RepositoryPath)
	}
	if task.ProjectID != p.ID || task.GoalID == "" || task.Title != validGoal {
		t.Errorf("task = %+v", task)
	}
	if len(task.Criteria) != 1 || task.Criteria[0] != "three arguments" {
		t.Errorf("criteria = %v", task.Criteria)
	}

	// Goal and task rows exist with FKs intact.
	var goalCount, taskCount int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM goals WHERE id = ?`, task.GoalID).Scan(&goalCount); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM tasks WHERE id = ?`, task.ID).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if goalCount != 1 || taskCount != 1 {
		t.Errorf("goal/task rows = %d/%d, want 1/1", goalCount, taskCount)
	}
}

func TestCreateValidation(t *testing.T) {
	r, _ := openRepo(t)
	ctx := context.Background()

	cases := []struct {
		name      string
		n, a, g   string
		errSubstr string
	}{
		{"bad archetype", "x", "astral", validGoal, "archetype"},
		{"empty name", "", "text", validGoal, "name"},
		{"short goal", "x", "text", "too short", "20"},
		{"empty goal", "x", "text", "", "20"},
	}
	for _, tc := range cases {
		if _, _, err := r.Create(ctx, tc.n, tc.a, tc.g, "", nil); err == nil || !strings.Contains(err.Error(), tc.errSubstr) {
			t.Errorf("%s: err = %v, want containing %q", tc.name, err, tc.errSubstr)
		}
	}

	// Duplicate names are rejected by the schema (UNIQUE).
	if _, _, err := r.Create(ctx, "demo", ArchetypeText, validGoal, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Create(ctx, "demo", ArchetypeCode, validGoal, "", nil); err == nil {
		t.Error("duplicate project name accepted, want UNIQUE rejection")
	}
}

func TestSetRepositoryPath(t *testing.T) {
	r, _ := openRepo(t)
	ctx := context.Background()
	p, _, err := r.Create(ctx, "demo", ArchetypeText, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetRepositoryPath(ctx, p.ID, "/tmp/repo"); err != nil {
		t.Fatalf("SetRepositoryPath: %v", err)
	}
	got, err := r.Get(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RepositoryPath != "/tmp/repo" {
		t.Errorf("repository path = %q, want /tmp/repo", got.RepositoryPath)
	}
	if err := r.SetRepositoryPath(ctx, "ghost", "/tmp/repo"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetRepositoryPath(missing) = %v, want ErrNotFound", err)
	}
}

func TestSubmitGoal(t *testing.T) {
	r, _ := openRepo(t)
	ctx := context.Background()
	p, _, err := r.Create(ctx, "demo", ArchetypeText, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	task, err := r.SubmitGoal(ctx, p.ID, "Summarize the essay in exactly five bullet points.", []string{"5 bullets"})
	if err != nil {
		t.Fatal(err)
	}
	if task.ProjectID != p.ID {
		t.Errorf("task project = %s, want %s", task.ProjectID, p.ID)
	}
	if len(task.Criteria) != 1 {
		t.Errorf("criteria = %v", task.Criteria)
	}

	// Unknown project and invalid goal text are rejected.
	if _, err := r.SubmitGoal(ctx, "ghost", validGoal, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("SubmitGoal(unknown project) = %v, want ErrNotFound", err)
	}
	if _, err := r.SubmitGoal(ctx, p.ID, "short", nil); err == nil || !strings.Contains(err.Error(), "20") {
		t.Errorf("SubmitGoal(short goal) = %v, want length error", err)
	}
}

func TestGetAndTaskMissing(t *testing.T) {
	r, _ := openRepo(t)
	if _, err := r.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}
	if _, err := r.Task(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Task(missing) = %v, want ErrNotFound", err)
	}
}

func TestValidArchetype(t *testing.T) {
	for _, a := range []string{ArchetypeText, ArchetypeCode, ArchetypeDocument, ArchetypeData, ArchetypeMedia} {
		if !ValidArchetype(a) {
			t.Errorf("ValidArchetype(%q) = false, want true", a)
		}
	}
	for _, a := range []string{"", "Text", "astral"} {
		if ValidArchetype(a) {
			t.Errorf("ValidArchetype(%q) = true, want false", a)
		}
	}
}
