package onboard

import (
	"strings"
	"testing"
)

func recByRole(recs []Recommendation, role string) Recommendation {
	for _, r := range recs {
		if r.Role == role {
			return r
		}
	}
	return Recommendation{}
}

func TestRouteJudgeCrossFamily(t *testing.T) {
	roles := []Role{
		{Role: "main", Model: "gemma4:12b-mlx"},
		{Role: "security", Model: "phi3:3.8b"},
	}
	families := map[string]string{
		"gemma4:12b-mlx": "gemma",
		"gemma4:e4b-mlx": "gemma",
		"granite4.2:3b":  "granite",
		"phi3:3.8b":      "phi3",
	}
	ms := []Measurement{
		{Role: "security", Model: "gemma4:12b-mlx", Score: 0.90}, // same family as main → excluded
		{Role: "security", Model: "granite4.2:3b", Score: 0.81},
		{Role: "security", Model: "phi3:3.8b", Score: 0.40},
	}
	recs := Route(roles, families, ms, RoutingOptions{
		JudgeRole: "security", GeneratorRoles: []string{"main", "tall"}, MinImprovement: 0.05,
	})
	sec := recByRole(recs, "security")
	if sec.Recommended != "granite4.2:3b" || !sec.Replace {
		t.Fatalf("security recommendation = %+v, want the cross-family granite4.2:3b", sec)
	}
}

func TestRouteReplaceAndKeep(t *testing.T) {
	roles := []Role{{Role: "main", Model: "ornith-1.5:9b"}}
	fams := map[string]string{"ornith-1.5:9b": "qwen35", "gemma4:12b-mlx": "gemma"}

	recs := Route(roles, fams, []Measurement{
		{Role: "main", Model: "ornith-1.5:9b", Score: 0.30},
		{Role: "main", Model: "gemma4:12b-mlx", Score: 0.60},
	}, RoutingOptions{MinImprovement: 0.10})
	if recs[0].Recommended != "gemma4:12b-mlx" || !recs[0].Replace {
		t.Fatalf("expected a replace with gemma4:12b-mlx: %+v", recs[0])
	}

	recs = Route(roles, fams, []Measurement{
		{Role: "main", Model: "ornith-1.5:9b", Score: 0.58},
		{Role: "main", Model: "gemma4:12b-mlx", Score: 0.60},
	}, RoutingOptions{MinImprovement: 0.10})
	if recs[0].Replace {
		t.Fatalf("expected the configured model kept within the margin: %+v", recs[0])
	}
}

func TestRouteNoDataKeepsConfigured(t *testing.T) {
	recs := Route([]Role{{Role: "wide", Model: "x"}}, nil, nil, RoutingOptions{})
	if len(recs) != 1 || recs[0].Replace || recs[0].Recommended != "x" ||
		!strings.Contains(recs[0].Reason, "no qualification data") {
		t.Fatalf("no-data recommendation = %+v", recs)
	}
}

// TestRouteJudgeUnknownFamilyExcluded: a judge candidate with an unknown family
// is not recommended — Route matches the engine's conservative posture.
func TestRouteJudgeUnknownFamilyExcluded(t *testing.T) {
	roles := []Role{{Role: "main", Model: "gemma4:12b-mlx"}, {Role: "security", Model: "phi3:3.8b"}}
	families := map[string]string{"gemma4:12b-mlx": "gemma", "granite4.2:3b": "granite"}
	ms := []Measurement{
		{Role: "security", Model: "mystery:7b", Score: 0.99}, // unknown family → excluded
		{Role: "security", Model: "granite4.2:3b", Score: 0.81},
	}
	recs := Route(roles, families, ms, RoutingOptions{
		JudgeRole: "security", GeneratorRoles: []string{"main"}, MinImprovement: 0.05,
	})
	if sec := recByRole(recs, "security"); sec.Recommended != "granite4.2:3b" {
		t.Fatalf("security = %+v, want granite4.2:3b (unknown-family judge excluded)", sec)
	}
}

// TestRouteFromM9Smoke encodes the numbers measured on the M2 Max (M9-T2b, see
// docs/qualification-smoke-m9.md) and pins what Route concludes:
//   - generator pass rate (dialectical): ornith-1.5:9b 0.33, gemma4:12b-mlx 0.80
//   - judge anchor agreement: granite4.2:3b 0.81, ornith-1.5:9b 0.77, gemma4:12b-mlx 0.69
func TestRouteFromM9Smoke(t *testing.T) {
	roles := []Role{
		{Role: "main", Model: "ornith-1.5:9b"},
		{Role: "security", Model: "phi3:3.8b"}, // unmeasured shipped default
	}
	families := map[string]string{
		"ornith-1.5:9b": "qwen35", "gemma4:12b-mlx": "gemma", "granite4.2:3b": "granite",
	}
	ms := []Measurement{
		{Role: "main", Model: "ornith-1.5:9b", Score: 0.33, Samples: 6},
		{Role: "main", Model: "gemma4:12b-mlx", Score: 0.80, Samples: 5},
		{Role: "security", Model: "granite4.2:3b", Score: 0.81, Samples: 16},
		{Role: "security", Model: "ornith-1.5:9b", Score: 0.77, Samples: 16}, // same family as main → excluded
		{Role: "security", Model: "gemma4:12b-mlx", Score: 0.69, Samples: 16},
	}
	recs := Route(roles, families, ms, RoutingOptions{
		JudgeRole: "security", GeneratorRoles: []string{"main", "tall"}, MinImprovement: 0.10, MinSamples: 3,
	})
	if got := recByRole(recs, "main"); got.Recommended != "gemma4:12b-mlx" || !got.Replace {
		t.Errorf("main = %+v, want gemma4:12b-mlx (0.80 vs 0.33)", got)
	}
	if got := recByRole(recs, "security"); got.Recommended != "granite4.2:3b" || !got.Replace {
		t.Errorf("security = %+v, want granite4.2:3b (0.81 cross-family; qwen35 self-judge excluded)", got)
	}
}

// TestRouteMinSamplesGuard: a candidate below the sample floor cannot replace the
// configured model — a single-pass pass@k is noisy (the M9 soak passed 6/6 then
// 0/2 on the same tasks).
func TestRouteMinSamplesGuard(t *testing.T) {
	roles := []Role{{Role: "main", Model: "ornith-1.5:9b"}}
	fams := map[string]string{"ornith-1.5:9b": "qwen35", "gemma4:12b-mlx": "gemma"}
	ms := []Measurement{
		{Role: "main", Model: "ornith-1.5:9b", Score: 0.30, Samples: 6},
		{Role: "main", Model: "gemma4:12b-mlx", Score: 0.90, Samples: 1}, // 1 sample → too noisy
	}
	recs := Route(roles, fams, ms, RoutingOptions{MinImprovement: 0.10, MinSamples: 3})
	if recs[0].Replace {
		t.Fatalf("replaced the configured model on a 1-sample measurement: %+v", recs[0])
	}
}
