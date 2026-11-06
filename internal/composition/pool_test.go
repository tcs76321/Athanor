package composition

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestChooseUsesMeasuredScore(t *testing.T) {
	p := New(nil)
	p.Upsert(ModelInfo{Name: "alpha", Family: "a", ContextTarget: 32768})
	p.Upsert(ModelInfo{Name: "beta", Family: "b", ContextTarget: 32768})
	p.SetMeasured("alpha", "code", 2, 5)
	p.SetMeasured("beta", "code", 9, 10)

	got, ok := p.Choose(Request{Capability: CapabilityCode, Stance: StanceGenerate})
	if !ok || got.Model != "beta" {
		t.Fatalf("choose = %+v ok=%v, want beta", got, ok)
	}
}

func TestChooseBreaksTiesByName(t *testing.T) {
	p := New(nil)
	for _, name := range []string{"zeta", "alpha", "mike"} {
		p.Upsert(ModelInfo{Name: name, ContextTarget: 32768})
	}
	got, ok := p.Choose(Request{Capability: CapabilityCode})
	if !ok || got.Model != "alpha" {
		t.Fatalf("tie-break = %q, want alpha", got.Model)
	}
}

func TestChooseSkipsTooLargeModels(t *testing.T) {
	p := New(nil)
	p.FitLimitBytes = 8 << 30
	p.Upsert(ModelInfo{Name: "huge", ContextTarget: 32768, SizeBytes: 30 << 30})
	p.Upsert(ModelInfo{Name: "small", ContextTarget: 32768, SizeBytes: 2 << 30})
	p.Sync([]ModelInfo{
		{Name: "huge", ContextTarget: 32768, SizeBytes: 30 << 30},
		{Name: "small", ContextTarget: 32768, SizeBytes: 2 << 30},
	})
	if got := p.EligibleNames(); len(got) != 1 || got[0] != "small" {
		t.Fatalf("eligible = %v, want only small", got)
	}
	got, ok := p.Choose(Request{Capability: CapabilityCode})
	if !ok || got.Model != "small" {
		t.Fatalf("choose = %q ok=%v, want small", got.Model, ok)
	}
}

func TestPoolDerivesMaxContextAndExcludesOnlyBelowFloor(t *testing.T) {
	p := New(nil)
	p.FitLimitBytes = 28 << 30
	p.Sync([]ModelInfo{
		{Name: "small", FileBytes: 3 << 30, SizeBytes: 3 << 30, NativeContext: 262144, KVBytesPerToken: 128 << 10, ContextTarget: 65536},
		{Name: "huge", FileBytes: 30 << 30, SizeBytes: 30 << 30, NativeContext: 262144, KVBytesPerToken: 128 << 10, ContextTarget: 65536},
	})
	if got := p.Profiles(); len(got) != 2 {
		t.Fatalf("profiles = %d, want 2", len(got))
	}
	byName := map[string]Profile{}
	for _, prof := range p.Profiles() {
		byName[prof.Name] = prof
	}
	small := byName["small"]
	if small.MaxContext <= 0 || small.MaxContext > 262144 || small.TooLarge {
		t.Errorf("small max = %d tooLarge=%v, want a usable bounded window", small.MaxContext, small.TooLarge)
	}
	if small.ContextTarget > small.MaxContext {
		t.Errorf("working context = %d exceeds max %d", small.ContextTarget, small.MaxContext)
	}
	if huge := byName["huge"]; !huge.TooLarge || huge.MaxContext != 0 {
		t.Errorf("huge = max %d tooLarge=%v, want excluded", huge.MaxContext, huge.TooLarge)
	}
}

func TestChoosePrefersSmallerModelBeforeMeasurement(t *testing.T) {
	p := New(nil)
	p.Upsert(ModelInfo{Name: "large", ContextTarget: 32768, SizeBytes: 9 << 30})
	p.Upsert(ModelInfo{Name: "small", ContextTarget: 32768, SizeBytes: 2 << 30})
	got, ok := p.Choose(Request{Capability: CapabilityCode})
	if !ok || got.Model != "small" {
		t.Fatalf("unmeasured tie chose %q, want the smaller model", got.Model)
	}
	// Authoring assist asks for the larger model on the same tie.
	got, ok = p.Choose(Request{Capability: CapabilityCode, PreferQuality: true})
	if !ok || got.Model != "large" {
		t.Fatalf("quality tie chose %q, want the larger model", got.Model)
	}
}

func TestPreferPanelNeedsEvidence(t *testing.T) {
	p := New(nil)
	p.Upsert(ModelInfo{Name: "a", ContextTarget: 32768})
	p.Upsert(ModelInfo{Name: "b", ContextTarget: 32768})
	if p.PreferPanel() {
		t.Fatal("cold start should not prefer a panel")
	}
	for i := 0; i < 4; i++ {
		p.RecordTemplate("panel", true)
		p.RecordTemplate("solo", false)
	}
	if !p.PreferPanel() {
		t.Fatal("measured panel advantage was ignored")
	}
}

func TestMachineScoreIsBoundedAndVisible(t *testing.T) {
	if got := ScoreMachine(0, 0, 0); got != 0 {
		t.Fatalf("empty machine score = %d, want 0", got)
	}
	got := ScoreMachine(32<<30, 100<<30, 10)
	if got < 50 || got > 100 {
		t.Fatalf("32 GiB / 10 CPU score = %d, want a sensible 50–100", got)
	}
}

func TestChooseRespectsContextFloor(t *testing.T) {
	p := New(nil)
	p.Upsert(ModelInfo{Name: "small", ContextTarget: 8192})
	p.Upsert(ModelInfo{Name: "large", ContextTarget: 65536})
	p.SetMeasured("small", "code", 10, 10)
	p.SetMeasured("large", "code", 1, 10)

	got, ok := p.Choose(Request{Capability: CapabilityCode, MinContext: 32768})
	if !ok || got.Model != "large" {
		t.Fatalf("context floor chose %q, want large", got.Model)
	}
}

func TestChoosePrefersAnotherFamilyForJudgment(t *testing.T) {
	p := New(nil)
	p.Upsert(ModelInfo{Name: "same", Family: "alpha", ContextTarget: 32768})
	p.Upsert(ModelInfo{Name: "other", Family: "beta", ContextTarget: 32768})
	p.SetMeasured("same", "judgment", 9, 10)
	p.SetMeasured("other", "judgment", 7, 10)

	got, ok := p.Choose(Request{Capability: CapabilityJudgment, AvoidFamily: "alpha"})
	if !ok || got.Model != "other" {
		t.Fatalf("family-independent choice = %q, want other", got.Model)
	}
}

func TestChooseManyPrefersDistinctFamilies(t *testing.T) {
	p := New(nil)
	p.Upsert(ModelInfo{Name: "a1", Family: "a", ContextTarget: 32768})
	p.Upsert(ModelInfo{Name: "a2", Family: "a", ContextTarget: 32768})
	p.Upsert(ModelInfo{Name: "b1", Family: "b", ContextTarget: 32768})
	p.SetMeasured("a1", "code", 9, 10)
	p.SetMeasured("a2", "code", 8, 10)
	p.SetMeasured("b1", "code", 7, 10)

	cs := p.ChooseMany(Request{Capability: CapabilityCode}, 2)
	if len(cs) != 2 || cs[0].Family == cs[1].Family {
		t.Fatalf("choose-many = %+v, want two distinct families", cs)
	}
}

func TestScopedEvidenceOutvotesOnlyWithEnoughSamples(t *testing.T) {
	p := New(nil)
	p.Upsert(ModelInfo{Name: "global", ContextTarget: 32768})
	p.Upsert(ModelInfo{Name: "local", ContextTarget: 32768})
	p.SetMeasured("global", "code", 6, 10)
	p.SetMeasured("local", "code", 3, 10)

	// One project result is not enough to override the parent prior.
	p.RecordScoped("local", "project:p1", "code", true)
	got, _ := p.Choose(Request{Capability: CapabilityCode, Scope: "project:p1"})
	if got.Model != "global" {
		t.Fatalf("one scoped sample overrode the prior: %q", got.Model)
	}

	// Ten project results carry enough weight to win.
	for i := 0; i < 10; i++ {
		p.RecordScoped("local", "project:p1", "code", true)
	}
	got, _ = p.Choose(Request{Capability: CapabilityCode, Scope: "project:p1"})
	if got.Model != "local" {
		t.Fatalf("scoped evidence did not win: %q", got.Model)
	}
}

func TestObserveUpdatesOnlyUsedModels(t *testing.T) {
	p := New(nil)
	p.Upsert(ModelInfo{Name: "alpha", ContextTarget: 32768})
	p.Upsert(ModelInfo{Name: "beta", ContextTarget: 32768})
	p.SetMeasured("alpha", "code", 5, 10)
	p.SetMeasured("beta", "code", 5, 10)

	p.Choose(Request{Capability: CapabilityCode, JobID: "job-1", Scope: "project:p"})
	p.Observe(context.Background(), "job-1", true)

	profiles := p.Profiles()
	for _, prof := range profiles {
		if prof.Name == "alpha" {
			if prof.Scores["code"].Trials != 11 || prof.Scores["code"].Successes != 6 {
				t.Fatalf("alpha not updated: %+v", prof.Scores["code"])
			}
		} else if prof.Scores["code"].Trials != 10 {
			t.Fatalf("unused beta was updated: %+v", prof.Scores["code"])
		}
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	st := testStore(t)
	p := New(st)
	p.Upsert(ModelInfo{Name: "alpha", Family: "a", ContextTarget: 32768})
	p.SetMeasured("alpha", "code", 3, 4)
	if err := p.Save(context.Background()); err != nil {
		t.Fatal(err)
	}

	again := New(st)
	if err := again.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	prof, ok := again.Profile("alpha")
	if !ok || prof.Scores["code"].Trials != 4 || prof.Scores["code"].Successes != 3 {
		t.Fatalf("round-trip = %+v", prof)
	}
}

func TestSyncChangedReportsRealChangesOnly(t *testing.T) {
	p := New(nil)
	infos := []ModelInfo{{Name: "alpha", Family: "a", SizeBytes: 1 << 30, ContextTarget: 8192}}
	added, changed := p.SyncChanged(infos)
	if added != 1 || !changed {
		t.Fatalf("first sync = added %d changed %v, want 1/true", added, changed)
	}
	added, changed = p.SyncChanged(infos)
	if added != 0 || changed {
		t.Fatalf("identical sync = added %d changed %v, want 0/false", added, changed)
	}
	infos[0].Family = "b"
	added, changed = p.SyncChanged(infos)
	if added != 0 || !changed {
		t.Fatalf("metadata change = added %d changed %v, want 0/true", added, changed)
	}
	added, changed = p.SyncChanged(nil)
	if added != 0 || !changed {
		t.Fatalf("removal = added %d changed %v, want 0/true", added, changed)
	}
	if p.Count() != 0 {
		t.Fatalf("count = %d, want 0", p.Count())
	}
}

func TestRemoveDropsModelAndMeasurements(t *testing.T) {
	p := New(nil)
	p.Upsert(ModelInfo{Name: "alpha"})
	p.SetMeasured("alpha", "code", 4, 4)
	p.Remove("alpha")
	if _, ok := p.Choose(Request{Capability: CapabilityCode}); ok {
		t.Fatal("removed model was still selected")
	}
	if p.Count() != 0 {
		t.Fatalf("count = %d", p.Count())
	}
}
