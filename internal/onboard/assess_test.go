package onboard

import "testing"

// TestAssessComposesInventoryAndRouting pins the M9-T2 production shape: a
// single call returns both the inventory and the (advisory) routing proposal,
// and an empty measurement set keeps every configured model with a reason.
func TestAssessComposesInventoryAndRouting(t *testing.T) {
	roles := []Role{
		{Role: "main", Model: "ornith-1.5:9b", ContextTarget: 32768},
		{Role: "security", Model: "phi3:3.8b", ContextTarget: 8192},
	}
	families := map[string]string{"ornith-1.5:9b": "qwen35", "phi3:3.8b": "phi"}
	models := []Model{
		{Name: "ornith-1.5:9b", SizeBytes: 7 << 30, Family: "qwen35"},
		{Name: "gemma4:12b-mlx", SizeBytes: 8 << 30, Family: "gemma"},
		{Name: "granite4.2:3b", SizeBytes: 2 << 30, Family: "granite"},
	}

	got := Assess(roles, families, models, 32<<30, 2<<30, nil, RoutingOptions{JudgeRole: "security"})
	if len(got.Report.Personas) != 2 {
		t.Fatalf("personas = %d, want 2", len(got.Report.Personas))
	}
	if !got.Report.Personas[0].Installed {
		t.Errorf("main configured model not marked installed")
	}
	if len(got.Recommendations) != 2 {
		t.Fatalf("recommendations = %d, want 2", len(got.Recommendations))
	}
	for _, r := range got.Recommendations {
		if r.Replace {
			t.Errorf("role %s replaced with no measurements: %+v", r.Role, r)
		}
	}
}
