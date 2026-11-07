package capacity

import (
	"errors"
	"testing"
)

func ctxModel(max int) ModelFit {
	return model("m", "fam", 2*gib, 96*1024, max, 0.7)
}

func TestPlanContextShortCode(t *testing.T) {
	p, err := PlanContext(ContextInput{
		Phase: "diverging", Capability: "code", Demand: DemandShort,
		PinnedTokens: 1500, WorkingSetTokens: 1000, OutputTokens: 4096,
		Model: ctxModel(32768),
		Floors: Floors{
			Planning: 8192, Judge: 8192,
			Demand:     map[Demand]int{DemandShort: 4096, DemandLong: 16384, DemandRepo: 32768},
			Capability: map[string]int{"code": 8192},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Floor != 8192 {
		t.Errorf("floor = %d, want 8192 (capability floor above short demand)", p.Floor)
	}
	if p.Target != 8192 {
		t.Errorf("target = %d, want 8192", p.Target)
	}
	if p.Safety == 0 {
		t.Error("safety = 0, want a nonzero margin")
	}
}

func TestPlanContextJudgeIgnoresRepoFloor(t *testing.T) {
	p, err := PlanContext(ContextInput{
		Phase: "evaluating", Capability: "code", Demand: DemandRepo,
		PinnedTokens: 2000, WorkingSetTokens: 0, OutputTokens: 1024,
		Model: ctxModel(32768),
		Floors: Floors{
			Planning: 8192, Judge: 8192,
			Demand:     map[Demand]int{DemandRepo: 32768},
			Capability: map[string]int{"code": 8192},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Floor != 8192 {
		t.Errorf("judge floor = %d, want 8192 (must not inherit repo demand)", p.Floor)
	}
	if p.Target > 8192 {
		t.Errorf("judge target = %d, want <= 8192", p.Target)
	}
}

func TestPlanContextRepoCodeGetsDeepWindow(t *testing.T) {
	p, err := PlanContext(ContextInput{
		Phase: "diverging", Capability: "code", Demand: DemandRepo,
		PinnedTokens: 2000, WorkingSetTokens: 6000, OutputTokens: 4096,
		Model: ctxModel(32768),
		Floors: Floors{
			Planning: 8192, Judge: 8192,
			Demand:     map[Demand]int{DemandRepo: 32768},
			Capability: map[string]int{"code": 8192},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Floor != 32768 {
		t.Errorf("floor = %d, want 32768", p.Floor)
	}
	if p.Target != 32768 {
		t.Errorf("target = %d, want 32768", p.Target)
	}
}

func TestPlanContextRefusesOverflow(t *testing.T) {
	_, err := PlanContext(ContextInput{
		Phase: "diverging", Capability: "code", Demand: DemandRepo,
		PinnedTokens: 9000, WorkingSetTokens: 1000, OutputTokens: 4096,
		Model: ctxModel(8192),
		Floors: Floors{
			Planning: 8192, Judge: 8192,
			Demand:     map[Demand]int{DemandRepo: 32768},
			Capability: map[string]int{"code": 8192},
		},
	})
	if !errors.Is(err, ErrContextUnavailable) {
		t.Fatalf("err = %v, want ErrContextUnavailable", err)
	}
}

func TestPlanContextUsesExactTargetWhenNoBucketFits(t *testing.T) {
	p, err := PlanContext(ContextInput{
		Phase: "diverging", Capability: "text", Demand: DemandShort,
		PinnedTokens: 2000, WorkingSetTokens: 5000, OutputTokens: 2000,
		Model:   ctxModel(12000),
		Buckets: []int{4096, 8192, 16384},
		Floors: Floors{
			Planning: 4096, Judge: 4096,
			Demand:     map[Demand]int{DemandShort: 4096},
			Capability: map[string]int{"text": 4096},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Target > 12000 {
		t.Errorf("target = %d exceeds model max 12000", p.Target)
	}
	if p.Bucket != p.Target {
		t.Errorf("bucket = %d, want the exact target %d (no bucket fits)", p.Bucket, p.Target)
	}
}

func TestInferDemand(t *testing.T) {
	cases := []struct {
		name string
		in   DemandInput
		want Demand
	}{
		{"short code", DemandInput{Capability: "code"}, DemandShort},
		{"repo code", DemandInput{Capability: "code", HasRepository: true}, DemandRepo},
		{"memory", DemandInput{Capability: "text", PriorMemory: true}, DemandMemory},
		{"long doc", DemandInput{Capability: "document", SourceCount: 2}, DemandLong},
		{"hint raises", DemandInput{Capability: "text", Hint: DemandLong}, DemandLong},
		{"hint cannot lower", DemandInput{Capability: "code", HasRepository: true, Hint: DemandShort}, DemandRepo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := InferDemand(tc.in); got != tc.want {
				t.Errorf("InferDemand = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEffectiveWorkingSet(t *testing.T) {
	if got := EffectiveWorkingSet(8192, 6000, 4096); got != 0 {
		t.Errorf("no room: got %d, want 0 (skip, never slice)", got)
	}
	if got := EffectiveWorkingSet(32768, 4000, 4096); got != 9868 {
		t.Errorf("room: got %d, want 9868", got)
	}
}
