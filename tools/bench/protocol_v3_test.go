package main

import (
	"strings"
	"testing"
)

// TestBenchProtocolV3 pins the 0.x instrument contract (ADR-0133): two
// generators, two judges, four distinct families, four claim suites, and the
// pre-registered thresholds. It also proves every pinned label is selectable by
// the runner and resolves to the declared family.
func TestBenchProtocolV3(t *testing.T) {
	p, err := loadProtocol("../../eval/bench/v3/protocol.yaml")
	if err != nil {
		t.Fatalf("loadProtocol v3: %v", err)
	}
	if p.Version != 3 || p.Benchmark != "v3" {
		t.Fatalf("version/benchmark = %d/%q, want 3/v3", p.Version, p.Benchmark)
	}
	if p.TaskDigest != "eval/bench/v3/CORPUS.digest" {
		t.Errorf("task digest = %q", p.TaskDigest)
	}

	wantGen := map[string]string{
		"bonsai27b": "bonsai-2:27b",
		"gemma12b":  "gemma4:12b-mlx",
	}
	wantJudge := map[string]string{
		"granite8b": "granite4.2:8b",
		"mimo9b":    "mimo-v2.6:9b-mxfp8",
	}
	byLabel := map[string]protocolRosterEntry{}
	for _, e := range p.Roster {
		byLabel[e.Label] = e
	}
	if len(p.Generators) != 2 {
		t.Fatalf("generators = %v, want 2", p.Generators)
	}
	for _, label := range p.Generators {
		e, ok := byLabel[label]
		if !ok || e.Model != wantGen[label] {
			t.Errorf("generator %q = %+v, want %q", label, e, wantGen[label])
		}
	}
	if len(p.Judges) != 2 {
		t.Fatalf("judges = %v, want 2", p.Judges)
	}
	for _, label := range p.Judges {
		e, ok := byLabel[label]
		if !ok || e.Model != wantJudge[label] {
			t.Errorf("judge %q = %+v, want %q", label, e, wantJudge[label])
		}
	}
	if len(p.Roster) != 4 {
		t.Errorf("roster = %d, want 4", len(p.Roster))
	}
	families := map[string]bool{}
	for _, e := range p.Roster {
		if e.Digest == "" {
			t.Errorf("roster %q has no digest", e.Label)
		}
		families[e.Family] = true
	}
	if len(families) != 4 {
		t.Errorf("families = %v, want 4 distinct", families)
	}

	// The in-loop judge must be the pinned granite8b, not the cross-check.
	if got := p.CanonicalPersonaSet.Roles["security"].Model; got != "granite4.2:8b" {
		t.Errorf("security model = %q, want granite4.2:8b", got)
	}

	for _, name := range []string{"core", "agentic", "quality", "one_shot"} {
		s, ok := p.Suites[name]
		if !ok {
			t.Errorf("missing suite %q", name)
			continue
		}
		if s.Tasks == "" || s.Passes < 1 || len(s.Arms) == 0 || s.HeldOut < 0 {
			t.Errorf("suite %q incomplete: %+v", name, s)
		}
	}
	if got := p.Suites["agentic"].Passes; got != 3 {
		t.Errorf("agentic passes = %d, want 3", got)
	}
	if got := p.Suites["quality"].Passes; got != 3 {
		t.Errorf("quality passes = %d, want 3", got)
	}
	if got := p.Suites["core"].Passes; got != 1 {
		t.Errorf("core passes = %d, want 1", got)
	}
	if got := p.Claims.NonInferiority.MarginPoints; got != 5 {
		t.Errorf("non-inferiority margin = %v, want 5", got)
	}
	if got := p.Claims.Quality.MinMeanDelta; got != 0.5 {
		t.Errorf("quality min mean delta = %v, want 0.5", got)
	}
	if got := p.Claims.EarlyStop.MinStopRate; got != 0.75 {
		t.Errorf("early-stop rate = %v, want 0.75", got)
	}

	// Every pinned label must be usable by the runner and resolve to its
	// declared family (the cross-family guard depends on it).
	for _, label := range append(append([]string{}, p.Generators...), p.Judges...) {
		m, ok := modelByLabel(label)
		if !ok {
			t.Errorf("label %q is not selectable by -model", label)
			continue
		}
		if m.Model != byLabel[label].Model {
			t.Errorf("label %q model = %q, want %q", label, m.Model, byLabel[label].Model)
		}
		if got := familyForTag(m.Model); got != m.Family {
			t.Errorf("familyForTag(%q) = %q, want %q", m.Model, got, m.Family)
		}
	}

	if _, ok := namedArm("cascade"); !ok {
		t.Error("cascade arm is missing")
	}
	if len(p.Claims.NonInferiority.Suites) != 2 {
		t.Errorf("non-inferiority suites = %v, want 2", p.Claims.NonInferiority.Suites)
	}
}

func TestValidateV3RejectsMissingGenerator(t *testing.T) {
	p, err := loadProtocol("../../eval/bench/v3/protocol.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p.Generators = p.Generators[:1]
	if err := validateV3(p); err == nil {
		t.Fatal("validateV3 accepted a protocol with one generator")
	}
}

func TestValidateV3RejectsSameFamily(t *testing.T) {
	p, err := loadProtocol("../../eval/bench/v3/protocol.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for i := range p.Roster {
		p.Roster[i].Family = "one-family"
	}
	if err := validateV3(p); err == nil {
		t.Fatal("validateV3 accepted a single-family matrix")
	}
}

// TestV3ConfigRendersPinnedMatrix proves the generated config uses each
// generator for its generation roles, the cross-family granite judge for
// security, and one_shot_first only on the cascade arm.
func TestV3ConfigRendersPinnedMatrix(t *testing.T) {
	for _, label := range []string{"bonsai27b", "gemma12b"} {
		m, ok := modelByLabel(label)
		if !ok {
			t.Fatalf("generator %q missing", label)
		}
		for _, armName := range []string{"single", "cascade", "full"} {
			a, ok := namedArm(armName)
			if !ok {
				t.Fatalf("arm %q missing", armName)
			}
			raw := probeConfigYAML(m, a, "off", "127.0.0.1:7420", false,
				probePolicy{JudgeModel: "granite4.2:8b"})
			if !strings.Contains(raw, `model: "`+m.Model+`"`) {
				t.Errorf("%s/%s config lacks generator %q", label, armName, m.Model)
			}
			if !strings.Contains(raw, `model: "granite4.2:8b"`) ||
				!strings.Contains(raw, `family: "granite"`) {
				t.Errorf("%s/%s config lacks the cross-family granite judge", label, armName)
			}
			hasOneShot := strings.Contains(raw, "one_shot_first: true")
			if wants := armName == "cascade"; hasOneShot != wants {
				t.Errorf("%s/%s one_shot_first = %v, want %v", label, armName, hasOneShot, wants)
			}
		}
	}
}
