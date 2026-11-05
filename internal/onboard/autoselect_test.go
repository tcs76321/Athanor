package onboard

import "testing"

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
