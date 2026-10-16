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
