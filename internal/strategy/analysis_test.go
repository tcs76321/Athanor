package strategy

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/cognitive"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

// seedCorpus creates n jobs under one code project, each with a profile whose
// diverging persona is `persona`, and an outcome whose acceptance is given by
// accepted. All confidences are equal so the directionally-consistent check
// passes for both polarities.
func seedCorpus(t *testing.T, s *store.Store, p project.Project, taskID, persona string, n int, accepted int) {
	t.Helper()
	ctx := context.Background()
	repo := NewRepo(s)
	jobs := job.NewRepository(s)
	for i := 0; i < n; i++ {
		j, err := jobs.Create(ctx, taskID, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateProfile(ctx, Profile{
			JobID: j.ID, ProjectID: p.ID, Archetype: p.Archetype,
			Signature: []SignatureEntry{
				{Phase: "planning", Persona: "tall", Temperature: 0.2},
				{Phase: "diverging", Persona: persona, Temperature: 0.8, Candidates: 3},
			},
		}); err != nil {
			t.Fatal(err)
		}
		result := ResultRejected
		if i < accepted {
			result = ResultAcceptedNew
		}
		if _, err := repo.CreateOutcome(ctx, Outcome{
			JobID: j.ID, Result: result, EvaluatorConfidence: 0.8, Score: 0.5,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMineSyntheticCorpus(t *testing.T) {
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
	p, task, err := projects.Create(ctx, "demo", project.ArchetypeCode,
		"Build a small REST endpoint with tests and documentation.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// alternative: 16/20 accepted (0.80); main: 8/20 (0.40). Baseline 0.60.
	seedCorpus(t, s, p, task.ID, "alternative", 20, 16)
	seedCorpus(t, s, p, task.ID, "main", 20, 8)

	repo := NewRepo(s)
	proposed, err := repo.Mine(ctx, MineOptions{MinCohortSize: 10, MinAcceptRateDelta: 0.15})
	if err != nil {
		t.Fatalf("Mine: %v", err)
	}
	byPersona := map[string]Insight{}
	for _, ins := range proposed {
		if ins.Pattern.Feature == "diverging.persona" {
			byPersona[ins.Pattern.Value] = ins
		}
	}
	alt, ok := byPersona["alternative"]
	if !ok || alt.Polarity != PolarityWinning || alt.Evidence.CohortJobs != 20 {
		t.Fatalf("alternative insight = %+v (ok=%v)", alt, ok)
	}
	mainIns, ok := byPersona["main"]
	if !ok || mainIns.Polarity != PolarityLosing {
		t.Fatalf("main insight = %+v (ok=%v)", mainIns, ok)
	}
	if alt.Evidence.AcceptRate < 0.79 || alt.Evidence.AcceptRate > 0.81 {
		t.Errorf("alternative accept rate = %v, want ~0.80", alt.Evidence.AcceptRate)
	}

	// Proposed insights are inert: no active statements.
	if stmts, _ := repo.ActiveStatements(ctx); len(stmts) != 0 {
		t.Fatalf("proposed insights leaked into active statements: %v", stmts)
	}

	// Promotion is explicit; re-mining does not duplicate.
	if err := repo.SetInsightStatus(ctx, alt.ID, InsightActive); err != nil {
		t.Fatal(err)
	}
	stmts, err := repo.ActiveStatements(ctx)
	if err != nil || len(stmts) != 1 {
		t.Fatalf("active statements = %v (err %v), want 1", stmts, err)
	}
	again, err := repo.Mine(ctx, MineOptions{MinCohortSize: 10, MinAcceptRateDelta: 0.15})
	if err != nil {
		t.Fatal(err)
	}
	for _, ins := range again {
		if ins.Pattern.Feature == "diverging.persona" && ins.Pattern.Value == "alternative" {
			t.Errorf("re-mining duplicated an existing insight: %+v", ins)
		}
	}
}

func TestMineRespectsCohortFloor(t *testing.T) {
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
	p, task, err := projects.Create(ctx, "demo", project.ArchetypeCode,
		"Build a small REST endpoint with tests and documentation.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	seedCorpus(t, s, p, task.ID, "alternative", 5, 5)
	if proposed, err := NewRepo(s).Mine(ctx, MineOptions{MinCohortSize: 10, MinAcceptRateDelta: 0.1}); err != nil || len(proposed) != 0 {
		t.Fatalf("below-floor corpus produced %d insights (err %v)", len(proposed), err)
	}
	if _, err := NewRepo(s).Mine(ctx, MineOptions{MinCohortSize: 0}); err == nil {
		t.Error("zero cohort floor accepted")
	}
}

// seedTechniqueCorpus creates n code jobs whose single outcome carries the
// given executed trajectory and accepted count, so technique mining has a
// grounded signal. All confidences are equal so the consistency check passes.
func seedTechniqueCorpus(t *testing.T, s *store.Store, p project.Project, taskID string, n, accepted int, ops []cognitive.Operation) {
	t.Helper()
	ctx := context.Background()
	repo := NewRepo(s)
	jobs := job.NewRepository(s)
	for i := 0; i < n; i++ {
		j, err := jobs.Create(ctx, taskID, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateProfile(ctx, Profile{
			JobID: j.ID, ProjectID: p.ID, Archetype: p.Archetype,
			Signature: []SignatureEntry{
				{Phase: "diverging", Persona: "main", Temperature: 0.8, Candidates: 3},
			},
		}); err != nil {
			t.Fatal(err)
		}
		result := ResultRejected
		if i < accepted {
			result = ResultAcceptedNew
		}
		if _, err := repo.CreateOutcome(ctx, Outcome{
			JobID: j.ID, Result: result, EvaluatorConfidence: 0.8, Score: 0.5, Operations: ops,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// mineCodeProject is the shared project setup for the technique-mining tests.
func mineCodeProject(t *testing.T) (*store.Store, project.Project, string) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	p, task, err := project.NewRepo(s).Create(context.Background(), "demo", project.ArchetypeCode,
		"Build a small REST endpoint with tests and documentation.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, p, task.ID
}

func TestMineTechniques(t *testing.T) {
	s, p, taskID := mineCodeProject(t)
	base := []cognitive.Operation{
		{Name: cognitive.OpPlan},
		{Name: cognitive.OpDiverge},
		{Name: cognitive.OpVerify, Grounded: true, Passed: true},
		{Name: cognitive.OpSynthesize},
		{Name: cognitive.OpCompare},
	}
	// 10 jobs accepted without reflection; 10 mostly-rejected with a
	// reflection pass. Baseline accept = 0.50; the reflection cohort = 0.20.
	seedTechniqueCorpus(t, s, p, taskID, 10, 8, base)
	withReflect := append(append([]cognitive.Operation{}, base...),
		cognitive.Operation{Name: cognitive.OpReflect})
	seedTechniqueCorpus(t, s, p, taskID, 10, 2, withReflect)

	proposed, err := NewRepo(s).Mine(context.Background(), MineOptions{MinCohortSize: 10, MinAcceptRateDelta: 0.15})
	if err != nil {
		t.Fatalf("Mine: %v", err)
	}
	var reflect *Insight
	for i := range proposed {
		if proposed[i].Pattern.Feature == TechniqueFeaturePrefix+cognitive.OpReflect {
			reflect = &proposed[i]
		}
	}
	if reflect == nil {
		t.Fatalf("no technique insight for the reflect operation; proposed=%+v", proposed)
	}
	if reflect.Pattern.Value != cognitive.OpReflect || reflect.Pattern.Context != "archetype="+p.Archetype {
		t.Errorf("technique pattern = %+v", reflect.Pattern)
	}
	if reflect.Polarity != PolarityLosing {
		t.Errorf("reflect polarity = %q, want %q", reflect.Polarity, PolarityLosing)
	}
	if reflect.Evidence.CohortJobs != 10 || reflect.Evidence.GroundedJobs == 0 {
		t.Errorf("technique evidence = %+v, want cohort 10 with grounded > 0", reflect.Evidence)
	}
	// Trajectories that ran only the common operations sit at the baseline and
	// produce no technique insight (no larger containing cohort contradicts).
	for _, ins := range proposed {
		if ins.Pattern.Feature == TechniqueFeaturePrefix+cognitive.OpPlan {
			t.Errorf("baseline-frequency operation produced a technique insight: %+v", ins)
		}
	}
}

// TestMineDoesNotDoubleCountRepeatedOperation proves the cohort-dedup fix: a
// trajectory that runs the same operation twice (e.g. two verify passes in a
// reflection loop) must contribute one cohort member, not two — otherwise the
// reported CohortJobs inflates and the "invent no numbers" promise breaks.
func TestMineDoesNotDoubleCountRepeatedOperation(t *testing.T) {
	s, p, taskID := mineCodeProject(t)
	// 20 accepted jobs, each trajectory running verify twice; 20 rejected
	// jobs with no verify. Baseline accept = 0.50; verify = 18/20 = 0.90.
	doubled := []cognitive.Operation{
		{Name: cognitive.OpVerify, Grounded: true, Passed: true},
		{Name: cognitive.OpVerify, Grounded: true, Passed: true},
	}
	seedTechniqueCorpus(t, s, p, taskID, 20, 18, doubled)
	plain := []cognitive.Operation{
		{Name: cognitive.OpPlan}, {Name: cognitive.OpDiverge}, {Name: cognitive.OpSynthesize}, {Name: cognitive.OpCompare},
	}
	seedTechniqueCorpus(t, s, p, taskID, 20, 2, plain)

	proposed, err := NewRepo(s).Mine(context.Background(), MineOptions{MinCohortSize: 10, MinAcceptRateDelta: 0.15})
	if err != nil {
		t.Fatalf("Mine: %v", err)
	}
	var verify *Insight
	for i := range proposed {
		if proposed[i].Pattern.Feature == TechniqueFeaturePrefix+cognitive.OpVerify {
			verify = &proposed[i]
		}
	}
	if verify == nil {
		t.Fatalf("no technique insight for verify; proposed=%+v", proposed)
	}
	if verify.Evidence.CohortJobs != 20 {
		t.Errorf("verify CohortJobs = %d, want 20 (a doubled operation must not double-count)", verify.Evidence.CohortJobs)
	}
}

func TestMineTechniquesRequireGrounded(t *testing.T) {
	s, p, taskID := mineCodeProject(t)
	// A would-be winning technique cohort with no deterministic verdict in its
	// trajectory must not be proposed as a technique (M8-T26; ADR-0067).
	ungrounded := []cognitive.Operation{
		{Name: cognitive.OpPlan},
		{Name: cognitive.OpDiverge},
		{Name: cognitive.OpReflect},
		{Name: cognitive.OpSynthesize},
		{Name: cognitive.OpCompare},
	}
	plain := []cognitive.Operation{
		{Name: cognitive.OpPlan},
		{Name: cognitive.OpDiverge},
		{Name: cognitive.OpSynthesize},
		{Name: cognitive.OpCompare},
	}
	seedTechniqueCorpus(t, s, p, taskID, 10, 9, ungrounded)
	seedTechniqueCorpus(t, s, p, taskID, 10, 1, plain)

	proposed, err := NewRepo(s).Mine(context.Background(), MineOptions{MinCohortSize: 10, MinAcceptRateDelta: 0.15})
	if err != nil {
		t.Fatalf("Mine: %v", err)
	}
	for _, ins := range proposed {
		if len(ins.Pattern.Feature) >= len(TechniqueFeaturePrefix) &&
			ins.Pattern.Feature[:len(TechniqueFeaturePrefix)] == TechniqueFeaturePrefix {
			t.Errorf("ungrounded cohort produced a technique insight: %+v", ins)
		}
	}
}

func TestActiveStatementsRendersTechniqueGuidance(t *testing.T) {
	s, _, _ := mineCodeProject(t)
	ctx := context.Background()
	repo := NewRepo(s)

	win, err := repo.CreateInsight(ctx, Insight{
		Scope: ScopeGlobal, Polarity: PolarityWinning,
		Pattern:   Pattern{Feature: TechniqueFeaturePrefix + cognitive.OpRefine, Value: cognitive.OpRefine, Context: "archetype=code"},
		Evidence:  Evidence{CohortJobs: 12, AcceptRate: 0.83, BaselineAcceptRate: 0.60, GroundedJobs: 12},
		Statement: "raw winning statement",
	})
	if err != nil {
		t.Fatal(err)
	}
	lose, err := repo.CreateInsight(ctx, Insight{
		Scope: ScopeGlobal, Polarity: PolarityLosing,
		Pattern:   Pattern{Feature: TechniqueFeaturePrefix + cognitive.OpReflect, Value: cognitive.OpReflect, Context: "archetype=code"},
		Evidence:  Evidence{CohortJobs: 10, AcceptRate: 0.20, BaselineAcceptRate: 0.50, GroundedJobs: 10},
		Statement: "raw losing statement",
	})
	if err != nil {
		t.Fatal(err)
	}
	persona, err := repo.CreateInsight(ctx, Insight{
		Scope: ScopeGlobal, Polarity: PolarityWinning,
		Pattern:   Pattern{Feature: "diverging.persona", Value: "alternative", Context: "archetype=code"},
		Evidence:  Evidence{CohortJobs: 20, AcceptRate: 0.80, BaselineAcceptRate: 0.60},
		Statement: "diverging.persona=alternative on code tasks: accept rate 0.80 vs 0.60 baseline (n=20).",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ins := range []Insight{win, lose, persona} {
		if err := repo.SetInsightStatus(ctx, ins.ID, InsightActive); err != nil {
			t.Fatal(err)
		}
	}
	stmts, err := repo.ActiveStatements(ctx)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(stmts, "\n")
	if !strings.Contains(joined, "prefer the 'refine' technique") {
		t.Errorf("winning technique not rendered as guidance:\n%s", joined)
	}
	if !strings.Contains(joined, "avoid the 'reflect' technique") {
		t.Errorf("losing technique not rendered as guidance:\n%s", joined)
	}
	if !strings.Contains(joined, "diverging.persona=alternative on code tasks") {
		t.Errorf("persona insight statement was altered:\n%s", joined)
	}
	if strings.Contains(joined, "raw ") {
		t.Errorf("raw statement leaked instead of guidance:\n%s", joined)
	}
}

func TestActivateWinningTechniques(t *testing.T) {
	s, _, _ := mineCodeProject(t)
	ctx := context.Background()
	repo := NewRepo(s)

	mk := func(feature, value, polarity string, ev Evidence) Insight {
		ins, err := repo.CreateInsight(ctx, Insight{
			Scope: ScopeGlobal, Polarity: polarity,
			Pattern:   Pattern{Feature: feature, Value: value, Context: "archetype=code"},
			Evidence:  ev,
			Statement: "s",
		})
		if err != nil {
			t.Fatal(err)
		}
		return ins
	}
	good := mk(TechniqueFeaturePrefix+cognitive.OpRefine, cognitive.OpRefine, PolarityWinning,
		Evidence{CohortJobs: 12, AcceptRate: 0.85, BaselineAcceptRate: 0.55, GroundedJobs: 12})
	weak := mk(TechniqueFeaturePrefix+cognitive.OpReflect, cognitive.OpReflect, PolarityWinning,
		Evidence{CohortJobs: 12, AcceptRate: 0.85, BaselineAcceptRate: 0.55, GroundedJobs: 1})
	lose := mk(TechniqueFeaturePrefix+cognitive.OpVerify, cognitive.OpVerify, PolarityLosing,
		Evidence{CohortJobs: 12, AcceptRate: 0.20, BaselineAcceptRate: 0.55, GroundedJobs: 12})
	persona := mk("diverging.persona", "alternative", PolarityWinning,
		Evidence{CohortJobs: 20, AcceptRate: 0.85, BaselineAcceptRate: 0.55})

	ids, err := repo.ActivateWinningTechniques(ctx, AutoActivateOptions{MinCohortSize: 10, MinAcceptRateDelta: 0.20, MaxActiveInsights: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != good.ID {
		t.Fatalf("activated = %v, want [%s]", ids, good.ID)
	}
	status := func(id string) string {
		ins, err := repo.GetInsight(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return ins.Status
	}
	if status(good.ID) != InsightActive {
		t.Errorf("grounded winning technique not activated: %q", status(good.ID))
	}
	for _, tc := range []struct {
		id, why string
	}{{weak.ID, "under-grounded"}, {lose.ID, "losing"}, {persona.ID, "persona (HITL-gated)"}} {
		if got := status(tc.id); got != InsightProposed {
			t.Errorf("%s insight activated (%q), want proposed", tc.why, got)
		}
	}

	// Bounded: once MaxActiveInsights is reached, no further activation.
	if _, err := repo.ActivateWinningTechniques(ctx, AutoActivateOptions{MinCohortSize: 0, MinAcceptRateDelta: 0, MaxActiveInsights: 1}); err != nil {
		t.Fatal(err)
	}
	if got := status(weak.ID); got != InsightProposed {
		t.Errorf("activation ignored the MaxActiveInsights bound: %q", got)
	}
}

func TestInsightExistsTreatsMutedDurable(t *testing.T) {
	s, _, _ := mineCodeProject(t)
	ctx := context.Background()
	repo := NewRepo(s)
	ins, err := repo.CreateInsight(ctx, Insight{
		Scope: ScopeGlobal, Polarity: PolarityWinning,
		Pattern:   Pattern{Feature: TechniqueFeaturePrefix + cognitive.OpRefine, Value: cognitive.OpRefine, Context: "archetype=code"},
		Statement: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetInsightStatus(ctx, ins.ID, InsightMuted); err != nil {
		t.Fatal(err)
	}
	if !repo.insightExists(ctx, ScopeGlobal, ins.Pattern) {
		t.Error("a muted insight's pattern must count as existing (mute must survive re-mining)")
	}
}
