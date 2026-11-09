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

func TestProjectPrioritySingleCritical(t *testing.T) {
	r, _ := openRepo(t)
	ctx := context.Background()
	p1, _, err := r.Create(ctx, "one", ArchetypeText, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p2, _, err := r.Create(ctx, "two", ArchetypeText, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Priority != PriorityMedium || p2.Priority != PriorityMedium {
		t.Fatalf("default priorities = %q/%q, want medium", p1.Priority, p2.Priority)
	}
	if err := r.SetPriority(ctx, p1.ID, PriorityHigh); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPriority(ctx, p1.ID, PriorityCritical); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPriority(ctx, p2.ID, PriorityCritical); err != nil {
		t.Fatal(err)
	}
	got1, _ := r.Get(ctx, p1.ID)
	got2, _ := r.Get(ctx, p2.ID)
	if got1.Priority != PriorityHigh || got2.Priority != PriorityCritical {
		t.Fatalf("priorities = %q/%q, want high/critical", got1.Priority, got2.Priority)
	}
	if err := r.SetPriority(ctx, p1.ID, "urgent"); err == nil {
		t.Fatal("invalid priority was accepted")
	}
}

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
		{"short goal", "x", "text", "ab", "3"},
		{"empty goal", "x", "text", "", "3"},
	}
	for _, tc := range cases {
		if _, _, err := r.Create(ctx, tc.n, tc.a, tc.g, "", nil); err == nil || !strings.Contains(err.Error(), tc.errSubstr) {
			t.Errorf("%s: err = %v, want containing %q", tc.name, err, tc.errSubstr)
		}
	}

	// An empty name is derived from the goal.
	p, _, err := r.Create(ctx, "", ArchetypeText, validGoal, "", nil)
	if err != nil {
		t.Fatalf("empty name should derive: %v", err)
	}
	if strings.TrimSpace(p.Name) == "" {
		t.Fatal("derived project name is empty")
	}
	// A duplicate name gets a numeric suffix rather than failing.
	if _, _, err := r.Create(ctx, p.Name, ArchetypeText, validGoal, "", nil); err != nil {
		t.Fatalf("duplicate name should be disambiguated: %v", err)
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
	if _, err := r.SubmitGoal(ctx, p.ID, "ab", nil); err == nil || !strings.Contains(err.Error(), "3") {
		t.Errorf("SubmitGoal(short goal) = %v, want length error", err)
	}
	// A short, honest goal is valid; onboarding supplies the depth.
	if _, err := r.SubmitGoal(ctx, p.ID, "a daily news brief", nil); err != nil {
		t.Errorf("SubmitGoal(a daily news brief) = %v, want accepted", err)
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

// TestCodeArchetypeGetsToolDefaults pins M9-T4: a code project's tasks carry
// the three execution tools so the real test command can run; non-code
// projects keep the daemon-wide default (no tools) and stay explicit.
func TestExecutionChecksDescribeIsDeterministic(t *testing.T) {
	var nilChecks *ExecutionChecks
	if got := nilChecks.Describe(); got != nil {
		t.Errorf("nil checks described as %v, want nil", got)
	}
	checks := &ExecutionChecks{
		MaxWords:         110,
		RequiredSections: []string{"Context"},
		ForbiddenPhrases: []string{"click here"},
		MustContainAny:   []string{"cannot", "unable"},
		MinItems:         []ItemCount{{Noun: "risks", Min: 2}},
		ArrayLength:      2,
		ArrayUniqueBy:    "id",
		ArraySortedBy:    "id",
		JSONFieldMatches: map[string]string{"created_at": `^\d{4}`},
	}
	lines := checks.Describe()
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"at most 110",
		`required section: "Context"`,
		`must not contain: "click here"`,
		`must contain at least one of: "cannot" → "unable"`,
		"at least 2 risks",
		"exactly 2 array item(s)",
		`unique by "id"`,
		`sorted ascending by "id"`,
		`field "created_at" must match regex`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("Describe() missing %q in:\n%s", want, joined)
		}
	}
	// The field order is stable across calls (sorted regex keys).
	if again := checks.Describe(); strings.Join(again, "\n") != joined {
		t.Errorf("Describe() is not deterministic:\n%s\n---\n%s", joined, strings.Join(again, "\n"))
	}
}

func TestCodeArchetypeGetsToolDefaults(t *testing.T) {
	r, _ := openRepo(t)
	ctx := context.Background()

	_, codeTask, err := r.Create(ctx, "code-demo", ArchetypeCode, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := CodeArchetypeTools()
	if len(codeTask.AllowedTools) != len(want) {
		t.Fatalf("code task tools = %v, want %v", codeTask.AllowedTools, want)
	}
	for i := range want {
		if codeTask.AllowedTools[i] != want[i] {
			t.Fatalf("code task tools = %v, want %v", codeTask.AllowedTools, want)
		}
	}

	codeProject, _, err := r.Create(ctx, "code-demo-2", ArchetypeCode, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	followUp, err := r.SubmitGoal(ctx, codeProject.ID, "Add a subtract function with tests.", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(followUp.AllowedTools) != len(want) {
		t.Errorf("follow-up code task tools = %v, want %v", followUp.AllowedTools, want)
	}

	_, textTask, err := r.Create(ctx, "text-demo", ArchetypeText, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(textTask.AllowedTools) != 0 {
		t.Errorf("text task tools = %v, want none", textTask.AllowedTools)
	}
}
