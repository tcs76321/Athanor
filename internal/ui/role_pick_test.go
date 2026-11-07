package ui

import (
	"testing"

	"github.com/tcs76321/athanor/internal/composition"
)

// TestMainIsSmallestTopOverall is the user-facing baseline rule: main is the
// smallest model in the highest overall tier, so a 1-point-lower larger model
// does not become main. Wide must be smaller than main.
func TestMainIsSmallestTopOverall(t *testing.T) {
	p := composition.New(nil)
	add := func(name string, size int64, successes, trials int) {
		p.Upsert(composition.ModelInfo{Name: name, SizeBytes: size, FileBytes: size, ContextTarget: 8192})
		p.SetMeasured(name, "general", successes, trials)
	}
	add("granite8", 5<<30, 82, 100)
	add("nemotron7", 6<<30, 82, 100)
	add("gemma12", 8<<30, 82, 100)
	add("mimo9", 8<<30, 81, 100)
	add("muse30", 18<<30, 84, 100)
	add("tiny2", 1<<30, 70, 100)
	u := &UI{deps: Deps{Pool: p}}
	picks := u.rolePicks()
	byRole := map[string]rolePick{}
	for _, pick := range picks {
		byRole[pick.Role] = pick
	}
	if byRole["main"].Model == "mimo9" {
		t.Fatalf("main = mimo9; want the smallest model in the top overall tier")
	}
	if byRole["main"].Model != "granite8" {
		t.Errorf("main = %q, want granite8 (smallest at the top overall score)", byRole["main"].Model)
	}
	if byRole["wide"].Model != "tiny2" {
		t.Errorf("wide = %q, want tiny2 (smaller than main)", byRole["wide"].Model)
	}
	if byRole["tall"].Model != "muse30" {
		t.Errorf("tall = %q, want muse30 (highest tall/overall)", byRole["tall"].Model)
	}
}

func TestPoolRecommendationAsksForFiveModels(t *testing.T) {
	p := composition.New(nil)
	for _, m := range []struct {
		name string
		size int64
	}{{"a", 2 << 30}, {"b", 5 << 30}, {"c", 12 << 30}} {
		p.Upsert(composition.ModelInfo{Name: m.name, SizeBytes: m.size, FileBytes: m.size})
	}
	u := &UI{deps: Deps{Pool: p}}
	v := u.welcomePool(nil)
	if v.Recommendation == "" {
		t.Fatal("Recommendation empty, want a five-model/family/size hint")
	}
}
