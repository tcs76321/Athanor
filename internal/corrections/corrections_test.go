package corrections

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func openCorrections(t *testing.T) (*Repo, *store.Store) {
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

func TestCaptureAllSourcesProduceRecords(t *testing.T) {
	repo, s := openCorrections(t)
	ctx := context.Background()

	sources := []Source{
		SourceUserRejection, SourceUserCorrection, SourceTestFailure,
		SourceEvaluatorFailure, SourceSecurityScan, SourceRuntimeError,
		SourceLoopDetection, SourceBudgetExhaustion, SourceHallucinatedPath,
	}
	for _, src := range sources {
		in := CaptureInput{Source: src, Detail: "detail for " + string(src)}
		if src == SourceUserRejection || src == SourceUserCorrection {
			// §18.4 mandatory form.
			in.Category = CategoryStyle
			in.Severity = SeverityHigh
			in.UserFeedback = "the code used global state"
			in.DerivedRule = "prefer explicit dependency injection"
			in.Scope = ScopeProject
		}
		rec, err := repo.Capture(ctx, in)
		if err != nil {
			t.Fatalf("Capture(%s): %v", src, err)
		}
		if rec.DerivedRule == "" {
			t.Errorf("%s: empty derived rule", src)
		}
		if !validCategory(rec.Category) || !validSeverity(rec.Severity) || !validScope(rec.Scope) {
			t.Errorf("%s: invalid fields %+v", src, rec)
		}
		if rec.Status != StatusActive {
			t.Errorf("%s: status = %q, want active", src, rec.Status)
		}
	}

	active, err := repo.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != len(sources) {
		t.Fatalf("active = %d, want %d", len(active), len(sources))
	}
	// Security corrections are global by default; a source table snapshot.
	for _, r := range active {
		if r.Category == CategorySecurity && r.Scope != ScopeGlobal {
			t.Errorf("security correction scope = %q, want global", r.Scope)
		}
	}

	events, err := s.QueryEvents(ctx, store.EventFilter{Category: "feedback"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != len(sources) {
		t.Errorf("feedback events = %d, want %d", len(events), len(sources))
	}
}

func TestCaptureRequiresFullUserForm(t *testing.T) {
	repo, _ := openCorrections(t)
	ctx := context.Background()
	_, err := repo.Capture(ctx, CaptureInput{Source: SourceUserRejection, Category: CategoryStyle})
	if !errors.Is(err, ErrIncompleteFeedback) {
		t.Fatalf("err = %v, want ErrIncompleteFeedback", err)
	}
	active, _ := repo.Active(ctx)
	if len(active) != 0 {
		t.Errorf("incomplete feedback persisted a record")
	}
}

func TestCaptureRejectsBadFields(t *testing.T) {
	repo, _ := openCorrections(t)
	ctx := context.Background()
	cases := []struct {
		name string
		in   CaptureInput
		want error
	}{
		{"unknown source", CaptureInput{Source: "bogus"}, ErrUnknownSource},
		{"bad category", CaptureInput{Source: SourceUserRejection, Category: "nope", Severity: SeverityLow, UserFeedback: "x", DerivedRule: "y", Scope: ScopeProject}, ErrInvalidField},
		{"bad severity", CaptureInput{Source: SourceUserRejection, Category: CategoryStyle, Severity: "nope", UserFeedback: "x", DerivedRule: "y", Scope: ScopeProject}, ErrInvalidField},
		{"bad scope", CaptureInput{Source: SourceUserRejection, Category: CategoryStyle, Severity: SeverityLow, UserFeedback: "x", DerivedRule: "y", Scope: "nope"}, ErrInvalidField},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := repo.Capture(ctx, c.in); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestSetStatusAndMarkApplied(t *testing.T) {
	repo, _ := openCorrections(t)
	ctx := context.Background()
	rec, err := repo.Capture(ctx, CaptureInput{Source: SourceTestFailure})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkApplied(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, rec.ID); got.AppliedCount != 1 {
		t.Errorf("applied count = %d, want 1", got.AppliedCount)
	}
	if err := repo.SetStatus(ctx, rec.ID, StatusMuted); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, rec.ID); got.Status != StatusMuted {
		t.Errorf("status = %q, want muted", got.Status)
	}
	if active, _ := repo.Active(ctx); len(active) != 0 {
		t.Errorf("muted record still active: %v", active)
	}
	if err := repo.SetStatus(ctx, rec.ID, "bogus"); !errors.Is(err, ErrInvalidField) {
		t.Errorf("bad status err = %v, want ErrInvalidField", err)
	}
}

func TestRelevantOrdersSeverityThenScopeAndScopesProject(t *testing.T) {
	repo, s := openCorrections(t)
	ctx := context.Background()

	projects := project.NewRepo(s)
	pa, _, err := projects.Create(ctx, "pa", project.ArchetypeText, "Write a short essay about local-first software.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	pb, _, err := projects.Create(ctx, "pb", project.ArchetypeText, "Write a different essay about local-first software.", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	mk := func(projectID, scope, severity string) Record {
		t.Helper()
		r, err := repo.Capture(ctx, CaptureInput{
			Source: SourceTestFailure, ProjectID: projectID, Scope: scope, Severity: severity,
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	low := mk(pa.ID, ScopeProject, SeverityLow)
	crit := mk(pa.ID, ScopeProject, SeverityCritical)
	highGlobal := mk(pa.ID, ScopeGlobal, SeverityHigh)
	mk(pb.ID, ScopeProject, SeverityCritical) // another project: excluded

	got, err := repo.Relevant(ctx, pa.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("relevant = %d, want 3 (project + global, not the other project)", len(got))
	}
	if got[0].ID != crit.ID || got[1].ID != highGlobal.ID || got[2].ID != low.ID {
		t.Errorf("order = %s/%s/%s, want critical-project, high-global, low-project",
			got[0].Severity, got[1].Severity, got[2].Severity)
	}

	capped, err := repo.Relevant(ctx, pa.ID, 2)
	if err != nil || len(capped) != 2 {
		t.Fatalf("capped relevant = %v (err %v), want 2", capped, err)
	}
}

func TestUpdateEditsFields(t *testing.T) {
	repo, _ := openCorrections(t)
	ctx := context.Background()
	rec, err := repo.Capture(ctx, CaptureInput{Source: SourceTestFailure})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repo.Update(ctx, rec.ID, EditInput{
		Category: CategoryStyle, Severity: SeverityLow, DerivedRule: "prefer small functions",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Category != CategoryStyle || updated.Severity != SeverityLow || updated.DerivedRule != "prefer small functions" {
		t.Errorf("updated = %+v", updated)
	}
	if _, err := repo.Update(ctx, rec.ID, EditInput{Category: "bogus"}); !errors.Is(err, ErrInvalidField) {
		t.Errorf("bad edit err = %v, want ErrInvalidField", err)
	}
}
