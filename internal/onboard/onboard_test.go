package onboard

import "testing"

func TestInventoryPresenceAndAlternatives(t *testing.T) {
	models := []Model{
		{Name: "gemma4:12b-mlx", SizeBytes: 8 << 30, Family: "gemma"},
		{Name: "granite4.2:30b", SizeBytes: 17 << 30, Family: "granite"},
		{Name: "ornith-1.5:9b", SizeBytes: 7 << 30, Family: "qwen35"},
		{Name: "muse-glimmer:30b-mlx", SizeBytes: 19 << 30, Family: "muse"},
	}
	roles := []Role{
		{Role: "main", Model: "ornith-1.5:9b", ContextTarget: 32768},
		{Role: "security", Model: "phi3:3.8b", ContextTarget: 8192}, // not installed
	}
	rep := Inventory(roles, models, 32<<30, 8<<30)
	if len(rep.Personas) != 2 {
		t.Fatalf("personas = %d", len(rep.Personas))
	}
	main := rep.Personas[0]
	if !main.Installed {
		t.Errorf("main model should be installed")
	}
	if len(main.Alternatives) != 3 {
		t.Errorf("main alternatives = %d, want 3: %+v", len(main.Alternatives), main.Alternatives)
	}
	if main.Alternatives[0].SizeBytes < main.Alternatives[1].SizeBytes {
		t.Errorf("alternatives not largest-first: %+v", main.Alternatives)
	}
	sec := rep.Personas[1]
	if sec.Installed {
		t.Errorf("security model should be marked missing")
	}
	if len(sec.Alternatives) != 4 {
		t.Errorf("security alternatives = %d, want 4", len(sec.Alternatives))
	}
}

func TestInventoryReserveExcludesLargeModels(t *testing.T) {
	models := []Model{
		{Name: "small:3b", SizeBytes: 2 << 30},
		{Name: "big:30b", SizeBytes: 26 << 30},
	}
	roles := []Role{{Role: "main", Model: "small:3b", ContextTarget: 32768}}
	// 32 GiB available, 8 GiB reserve → only 24 GiB fits; big (26 GiB) excluded.
	rep := Inventory(roles, models, 32<<30, 8<<30)
	for _, a := range rep.Personas[0].Alternatives {
		if a.Name == "big:30b" {
			t.Errorf("a model that does not fit was listed as an alternative")
		}
	}
}

func TestInventoryTagInsensitivePresence(t *testing.T) {
	models := []Model{{Name: "qwen2.5:14b", SizeBytes: 9 << 30}}
	roles := []Role{{Role: "main", Model: "qwen2.5:7b", ContextTarget: 32768}}
	rep := Inventory(roles, models, 32<<30, 8<<30)
	if !rep.Personas[0].Installed {
		t.Error("a differently-tagged model of the same base should count as installed")
	}
	if len(rep.Personas[0].Alternatives) != 0 {
		t.Errorf("the same-base model should not be an alternative: %+v", rep.Personas[0].Alternatives)
	}
}
