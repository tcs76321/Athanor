package onboard

import (
	"strings"
	"testing"
)

func TestAutoAssignPicksLargestAndCrossFamily(t *testing.T) {
	roles := []Role{
		{Role: "wide", ContextTarget: 8192},
		{Role: "tall", ContextTarget: 32768},
		{Role: "main", ContextTarget: 32768},
		{Role: "security", ContextTarget: 32768},
		{Role: "alternative", ContextTarget: 32768},
	}
	models := []Model{
		{Name: "small-fast:3b", SizeBytes: 2 << 30, Family: "granite"},
		{Name: "mid:8b", SizeBytes: 5 << 30, Family: "qwen"},
		{Name: "large:14b", SizeBytes: 9 << 30, Family: "gemma"},
		{Name: "alt:12b", SizeBytes: 8 << 30, Family: "muse"},
	}
	got := AutoAssign(roles, models, 32<<30, 8<<30)
	if len(got) != 5 {
		t.Fatalf("assignments = %d, want 5", len(got))
	}
	byRole := map[string]Assignment{}
	for _, a := range got {
		byRole[a.Role] = a
	}
	if byRole["tall"].Model != "large:14b" {
		t.Errorf("tall = %q, want largest", byRole["tall"].Model)
	}
	if byRole["wide"].Model != "small-fast:3b" {
		t.Errorf("wide = %q, want smallest", byRole["wide"].Model)
	}
	gen := map[string]bool{
		byRole["wide"].Family: true, byRole["tall"].Family: true,
		byRole["main"].Family: true, byRole["alternative"].Family: true,
	}
	if gen[byRole["security"].Family] {
		t.Errorf("security family %q is also a generator family", byRole["security"].Family)
	}
	if byRole["alternative"].Family == byRole["tall"].Family && byRole["alternative"].Family == byRole["main"].Family {
		t.Errorf("alternative did not try a second family: %+v", byRole["alternative"])
	}
}

func TestAutoAssignSingleModel(t *testing.T) {
	roles := []Role{{Role: "wide"}, {Role: "tall"}, {Role: "main"}, {Role: "security"}, {Role: "alternative"}}
	models := []Model{{Name: "only:7b", SizeBytes: 4 << 30, Family: "gemma"}}
	got := AutoAssign(roles, models, 0, 0)
	if len(got) != 5 {
		t.Fatalf("assignments = %d", len(got))
	}
	for _, a := range got {
		if a.Model != "only:7b" {
			t.Errorf("%s = %q, want only model", a.Role, a.Model)
		}
	}
	if got[3].Reason == "" {
		t.Error("security assignment lacks a reason")
	}
}

func TestAutoAssignUsesMeasuredScores(t *testing.T) {
	roles := []Role{{Role: "tall"}, {Role: "main"}, {Role: "security"}, {Role: "alternative"}}
	models := []Model{
		{Name: "large", SizeBytes: 9 << 30, Family: "gemma"},
		{Name: "mid", SizeBytes: 5 << 30, Family: "qwen"},
		{Name: "other", SizeBytes: 4 << 30, Family: "muse"},
	}
	scores := map[string]float64{
		"tall|mid": 0.9, "tall|large": 0.4,
		"main|large": 0.8, "main|mid": 0.2,
	}
	got := AutoAssignWithScores(roles, models, scores, 32<<30, 2<<30)
	by := map[string]Assignment{}
	for _, a := range got {
		by[a.Role] = a
	}
	if by["tall"].Model != "mid" {
		t.Errorf("tall = %q, want the higher measured model mid", by["tall"].Model)
	}
	if by["main"].Model != "large" {
		t.Errorf("main = %q, want large (measured 0.8)", by["main"].Model)
	}
	if by["security"].Family == by["tall"].Family || by["security"].Family == by["main"].Family {
		t.Errorf("security family = %q, must differ from generators", by["security"].Family)
	}
}

func TestEmbeddingModelsAreNotPersonaCandidates(t *testing.T) {
	models := []Model{
		{Name: "nomic-embed-text:latest", SizeBytes: 274 << 20, Family: "nomic-bert"},
		{Name: "gemma4:12b-mlx", SizeBytes: 8 << 30},
		{Name: "granite4.2:3b", SizeBytes: 3 << 30, Family: "granite"},
	}
	roles := []Role{
		{Role: "wide"}, {Role: "tall"}, {Role: "main"},
		{Role: "security"}, {Role: "alternative"},
	}
	got := AutoAssign(roles, models, 64<<30, 2<<30)
	if len(got) == 0 {
		t.Fatal("no assignments")
	}
	for _, a := range got {
		if a.Model == "nomic-embed-text:latest" {
			t.Errorf("embedding model assigned to persona %q", a.Role)
		}
	}
	for _, a := range got {
		if a.Role == "wide" && a.Model != "granite4.2:3b" {
			t.Errorf("wide = %q, want the smallest chat model granite4.2:3b", a.Model)
		}
	}
	if IsChatModel(models[0]) {
		t.Error("nomic-embed-text should not be a chat model")
	}
}

func TestAutoAssignUnknownMemoryStillSelects(t *testing.T) {
	roles := []Role{{Role: "tall"}, {Role: "security"}}
	models := []Model{
		{Name: "a:7b", SizeBytes: 4 << 30, Family: "qwen"},
		{Name: "b:7b", SizeBytes: 5 << 30, Family: "gemma"},
	}
	got := AutoAssign(roles, models, 0, 0)
	if len(got) != 2 || got[0].Model == "" || got[1].Model == "" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Model != "b:7b" {
		t.Errorf("tall = %q, want largest", got[0].Model)
	}
}

func castByRole(got []Assignment) map[string]Assignment {
	m := map[string]Assignment{}
	for _, a := range got {
		m[a.Role] = a
	}
	return m
}

// TestMainIsSmallestTopTier pins the baseline-cast rule: a one-point score
// difference does not justify a larger resident model. The smallest model in
// the highest measured main tier wins; wide must be genuinely smaller.
func TestMainIsSmallestTopTier(t *testing.T) {
	roles := []Role{{Role: "wide"}, {Role: "tall"}, {Role: "main"}, {Role: "security"}, {Role: "alternative"}}
	models := []Model{
		{Name: "big12b", SizeBytes: 8 << 30, Family: "gemma"},
		{Name: "mid8b", SizeBytes: 5 << 30, Family: "granite"},
		{Name: "small4b", SizeBytes: 2 << 30, Family: "nemotron"},
		{Name: "tiny2b", SizeBytes: 1 << 30, Family: "lfm"},
		{Name: "huge30b", SizeBytes: 18 << 30, Family: "muse"},
	}
	scores := map[string]float64{
		"main|big12b": 0.82, "main|mid8b": 0.82, "main|small4b": 0.82,
		"main|tiny2b": 0.70, "main|huge30b": 0.81,
		"wide|tiny2b": 0.80, "wide|small4b": 0.70,
		"tall|huge30b": 0.90, "tall|big12b": 0.82,
		"security|mid8b": 0.90,
	}
	by := castByRole(AutoAssignWithScores(roles, models, scores, 64<<30, 2<<30))
	if by["main"].Model != "small4b" {
		t.Errorf("main = %q, want small4b (smallest of the 82%% tier)", by["main"].Model)
	}
	if by["wide"].Model != "tiny2b" {
		t.Errorf("wide = %q, want tiny2b (smaller than main)", by["wide"].Model)
	}
	if by["tall"].Model != "huge30b" {
		t.Errorf("tall = %q, want huge30b (highest tall score)", by["tall"].Model)
	}
}

func TestWideMustBeSmallerThanMain(t *testing.T) {
	roles := []Role{{Role: "wide"}, {Role: "tall"}, {Role: "main"}, {Role: "security"}, {Role: "alternative"}}
	models := []Model{
		{Name: "main8b", SizeBytes: 5 << 30, Family: "granite"},
		{Name: "other8b", SizeBytes: 5 << 30, Family: "gemma"},
		{Name: "big30b", SizeBytes: 18 << 30, Family: "muse"},
	}
	scores := map[string]float64{
		"main|main8b": 0.85, "main|other8b": 0.80,
		"wide|other8b": 0.90, "wide|main8b": 0.80,
		"tall|big30b": 0.90,
	}
	by := castByRole(AutoAssignWithScores(roles, models, scores, 64<<30, 2<<30))
	// No model is smaller than main; wide must fall back to main with a reason.
	if by["wide"].Model != by["main"].Model {
		t.Errorf("wide = %q, main = %q; want wide to fall back to main when nothing is smaller", by["wide"].Model, by["main"].Model)
	}
	if !strings.Contains(by["wide"].Reason, "no smaller model") {
		t.Errorf("wide reason = %q, want an explicit smaller-model hint", by["wide"].Reason)
	}
}

func TestWidePrefersDifferentFamily(t *testing.T) {
	roles := []Role{{Role: "wide"}, {Role: "tall"}, {Role: "main"}, {Role: "security"}, {Role: "alternative"}}
	models := []Model{
		{Name: "main8b", SizeBytes: 5 << 30, Family: "granite"},
		{Name: "smallA3b", SizeBytes: 2 << 30, Family: "granite"},
		{Name: "smallB3b", SizeBytes: 2 << 30, Family: "nemotron"},
		{Name: "tall30b", SizeBytes: 18 << 30, Family: "muse"},
	}
	scores := map[string]float64{
		"main|main8b":   0.85,
		"wide|smallA3b": 0.80, "wide|smallB3b": 0.78,
		"tall|tall30b": 0.90, "security|smallA3b": 0.85,
	}
	by := castByRole(AutoAssignWithScores(roles, models, scores, 64<<30, 2<<30))
	if by["wide"].Model != "smallB3b" {
		t.Errorf("wide = %q, want smallB3b (different family from main)", by["wide"].Model)
	}
}
