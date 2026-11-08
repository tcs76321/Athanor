package onboard

import "testing"

func rosterEv(p float64) Evidence {
	return Evidence{Successes: int(p * 100), Trials: 100}
}

func rosterCaps(values map[string]float64) map[string]Evidence {
	out := map[string]Evidence{}
	for capability, value := range values {
		out[capability] = rosterEv(value)
	}
	return out
}

func TestInferFamilyFromNameWhenCatalogIsBlank(t *testing.T) {
	cases := map[string]string{
		"qwen3.8:27b-mlx":      "qwen3.8",
		"gemma4:12b-mlx":       "gemma4",
		"muse-glimmer:30b-mlx": "muse-glimmer",
		"bonsai-2:9b":          "bonsai-2",
		"granite4.2:30b":       "granite4.2",
	}
	for name, want := range cases {
		if got := InferFamily("", name); got != want {
			t.Errorf("InferFamily(%q) = %q, want %q", name, got, want)
		}
	}
	if got := InferFamily("qwen35", "bonsai-2:9b"); got != "bonsai-2" {
		t.Errorf("name-base family = %q, want bonsai-2", got)
	}
	if got := InferFamily("explicit", ""); got != "explicit" {
		t.Errorf("reported fallback = %q, want explicit", got)
	}
}

func TestRosterChoosesFunctionalWorkers(t *testing.T) {
	models := []RosterModel{
		{
			Name: "qwen3.8:27b-mlx", Family: "qwen3.8", SizeBytes: 17 * (1 << 30), ContextTarget: 12288, Overall: .83,
			Capabilities: rosterCaps(map[string]float64{
				"general": .83, "code": .80, "data": .80, "judgment": .97,
				"summarize": .67, "security": .90, "agentry": .90,
				"planning": .85, "calibration": .85, "repair": .85,
			}),
		},
		{
			Name: "muse-glimmer:30b-mlx", Family: "muse-glimmer", SizeBytes: 18 * (1 << 30), ContextTarget: 12288, Overall: .82,
			Capabilities: rosterCaps(map[string]float64{
				"general": .82, "code": .90, "data": .80, "judgment": .93,
				"summarize": .78, "security": .90, "agentry": .90,
				"planning": .82, "calibration": .82, "repair": .82,
			}),
		},
		{
			Name: "bonsai-2:27b", Family: "bonsai-2", SizeBytes: 8 * (1 << 30), ContextTarget: 32768, Overall: .81,
			Capabilities: rosterCaps(map[string]float64{
				"general": .81, "code": .80, "data": .73, "judgment": .93,
				"summarize": .89, "security": .80, "agentry": .80,
			}),
		},
		{
			Name: "gemma4:12b-mlx", Family: "gemma4", SizeBytes: 7 * (1 << 30), ContextTarget: 32768, Overall: .81,
			Capabilities: rosterCaps(map[string]float64{
				"general": .81, "code": .80, "data": .80, "judgment": .91,
				"summarize": .89, "security": .60, "agentry": .90,
			}),
		},
		{
			Name: "ornith-1.5:9b", Family: "ornith-1.5", SizeBytes: 6 * (1 << 30), ContextTarget: 32768, Overall: .80,
			Capabilities: rosterCaps(map[string]float64{
				"general": .80, "code": .85, "data": .80, "judgment": .95,
				"summarize": .78, "security": .90, "agentry": .80,
			}),
		},
		{
			Name: "bonsai-2:9b", Family: "bonsai-2", SizeBytes: 3 * (1 << 30), ContextTarget: 65536, Overall: .80,
			Capabilities: rosterCaps(map[string]float64{
				"general": .80, "code": .80, "data": .80, "judgment": .91,
				"summarize": .67, "security": .70, "agentry": .80,
			}),
		},
	}
	roster := BuildRoster(models, 27*(1<<30))
	for _, pick := range roster.Picks {
		t.Logf("%s: %s (%.0f%%, %d samples) — %s", pick.Role, pick.Model, pick.Quality*100, pick.Samples, pick.Reason)
	}
	for _, leader := range roster.Leaders {
		t.Logf("%s leader: %s %.0f%% (runner-up %s)", leader.Capability, leader.Model, leader.Score*100, leader.RunnerUp)
	}
	for _, experiment := range roster.Experiments {
		t.Logf("experiment: %s — %s", experiment.Model, experiment.Reason)
	}
	picks := map[string]RosterPick{}
	for _, pick := range roster.Picks {
		picks[pick.Role] = pick
	}
	for _, role := range []string{"main", "tall", "wide", "security", "alternative"} {
		if _, ok := picks[role]; !ok {
			t.Fatalf("roster missing %s: %+v", role, roster.Picks)
		}
	}
	if got := picks["tall"].Model; got != "qwen3.8:27b-mlx" {
		t.Errorf("architect = %q, want qwen3.8", got)
	}
	if got := picks["main"].Model; got != "gemma4:12b-mlx" {
		t.Errorf("worker = %q, want gemma4:12b", got)
	}
	if got := picks["wide"].Model; got != "bonsai-2:9b" {
		t.Errorf("scout = %q, want bonsai-2:9b", got)
	}
	if len(roster.Leaders) == 0 {
		t.Fatal("no capability leaders")
	}
	for _, leader := range roster.Leaders {
		if leader.Capability == "code" && leader.Model != "muse-glimmer:30b-mlx" {
			t.Errorf("code leader = %q, want muse-glimmer", leader.Model)
		}
	}
	if len(roster.Experiments) == 0 {
		t.Fatal("no exploration candidates")
	}
}

func TestRosterFillsAllRolesOnSmallPools(t *testing.T) {
	roster := BuildRoster([]RosterModel{{
		Name: "solo:3b", Family: "solo", SizeBytes: 2 << 30, ContextTarget: 32768,
		Capabilities: rosterCaps(map[string]float64{"general": .7, "code": .7}),
	}}, 8<<30)
	if len(roster.Picks) != 5 {
		t.Fatalf("picks = %+v, want five roles", roster.Picks)
	}
	for _, pick := range roster.Picks {
		if pick.Model != "solo:3b" {
			t.Errorf("%s = %q, want the only model", pick.Role, pick.Model)
		}
	}
}
