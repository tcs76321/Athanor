package capacity

import "testing"

const (
	gib = int64(1 << 30)
	mib = int64(1 << 20)
)

func model(name, family string, weights, kvPerToken int64, maxCtx int, score float64) ModelFit {
	return ModelFit{
		Name: name, Family: family, WeightsBytes: weights,
		KVPerToken: kvPerToken, NativeContext: maxCtx, MaxContext: maxCtx, Score: score,
	}
}

// m2Max is the reference 32 GiB profile: a 12 GiB Podman VM, three slots, and
// a mix of small, medium, and large models.
func m2Max() Inputs {
	return Inputs{
		HostBytes: 32 * gib, RuntimeBytes: 12 * gib, FreeDiskBytes: 200 * gib, CPUs: 12,
		Models: []ModelFit{
			model("tiny1b", "alpha", 1*gib, 64*1024, 32768, 0.40),
			model("small3b", "beta", 2*gib, 96*1024, 32768, 0.70),
			model("big9b", "gamma", 6*gib, 128*1024, 32768, 0.85),
		},
		EmbeddingModel: "embed", EmbeddingBytes: 300 * mib,
		JudgeModel: "small3b", JudgeBytes: 0,
		Podman: true, JobPodImage: "athanor-jobpod:latest",
		CoreReserveBytes: 2 * gib, HostReserveBytes: 6 * gib,
		PerPodBytes: 2 * gib, PerSlotOverheadBytes: 64 * mib,
		MaxJobs: 3, MaxLLMCalls: 3,
		HeadroomPercent: 20, SwarmTaskRatio: 0.5,
		RepoScratchBytes: 2 * gib,
	}
}

func TestM2MaxPlansSoloAndSwarm(t *testing.T) {
	rep := PlanMachine(m2Max())
	if rep.Solo == nil {
		t.Fatal("solo = nil, want the 9B plan")
	}
	if rep.Solo.Model != "big9b" {
		t.Errorf("solo model = %q, want big9b (highest score that fits)", rep.Solo.Model)
	}
	if rep.Solo.Context != 32768 {
		t.Errorf("solo context = %d, want 32768", rep.Solo.Context)
	}
	if rep.SwarmPods == nil {
		t.Fatal("SwarmPods = nil, want a 3-slot homogeneous swarm")
	}
	if rep.SwarmPods.Slots != 3 {
		t.Errorf("swarm slots = %d, want 3 (VM/operator bound)", rep.SwarmPods.Slots)
	}
	if got := rep.SwarmPods.Groups[0].Model; got != "small3b" {
		t.Errorf("swarm model = %q, want small3b (cheapest adequate)", got)
	}
	if got := rep.SwarmPods.Context; got != 8192 {
		t.Errorf("swarm context = %d, want 8192 (3 slots × 8k)", got)
	}
	if !rep.Coverage.CrossFamilyJudge {
		t.Error("CrossFamilyJudge = false, want true (three families)")
	}
	if !rep.Coverage.CodeExecution {
		t.Error("CodeExecution = false, want true")
	}
	if len(rep.Coverage.MissingRoles) != 0 {
		t.Errorf("MissingRoles = %v, want none", rep.Coverage.MissingRoles)
	}
}

func TestOneModelMachineIsHonest(t *testing.T) {
	in := Inputs{
		HostBytes: 8 * gib, RuntimeBytes: 4 * gib, FreeDiskBytes: 40 * gib, CPUs: 4,
		Models: []ModelFit{model("only3b", "beta", 2*gib, 96*1024, 8192, 0.60)},
		// No embedding model, no Podman: a text-only laptop.
		Podman: false, CoreReserveBytes: 1500 * mib, HostReserveBytes: 4 * gib,
		PerPodBytes: 1500 * mib, MaxJobs: 2, MaxLLMCalls: 2,
		HeadroomPercent: 20, SwarmTaskRatio: 0.5,
	}
	rep := PlanMachine(in)
	if rep.Solo == nil {
		t.Fatal("solo = nil, want a podless 3B plan")
	}
	if !rep.Coverage.SingleModel {
		t.Error("SingleModel = false, want true")
	}
	if rep.Coverage.CodeExecution {
		t.Error("CodeExecution = true without Podman, want false")
	}
	if rep.Coverage.Embedding || rep.Coverage.VectorMemory {
		t.Error("embedding/vector memory = true, want false")
	}
	if rep.Coverage.CrossFamilyJudge {
		t.Error("CrossFamilyJudge = true with one model, want false")
	}
	if got := rep.Coverage.MissingRoles; len(got) == 0 {
		t.Error("MissingRoles = none, want wide/tall/alternative")
	}
	if rep.Coverage.Capabilities["code"] {
		t.Error("code capability = true without a Job Pod runtime, want false")
	}
	if !rep.Coverage.Capabilities["text"] || !rep.Coverage.Capabilities["document"] {
		t.Error("text/document capabilities = false, want true")
	}
	if hasReason(rep, "no_code_execution") == false {
		t.Error("missing no_code_execution reason")
	}
	if hasReason(rep, "single_model") == false {
		t.Error("missing single_model reason")
	}
	if hasReason(rep, "no_embedding") == false {
		t.Error("missing no_embedding reason")
	}
}

func TestNoModelFits(t *testing.T) {
	rep := PlanMachine(Inputs{
		HostBytes: 4 * gib, HostReserveBytes: 3 * gib,
		Models:  []ModelFit{model("huge", "x", 8*gib, 128*1024, 32768, 0.9)},
		MaxJobs: 1, MaxLLMCalls: 1,
	})
	if rep.Solo != nil {
		t.Fatalf("solo = %+v, want nil", rep.Solo)
	}
	if !hasReason(rep, "no_model") {
		t.Error("missing no_model reason")
	}
}

func TestEligibleForSwarmRequiresSmallerTasks(t *testing.T) {
	rep := PlanMachine(m2Max())
	solo := *rep.Solo
	small := TaskEstimate{Capability: "text", Demand: DemandShort, Difficulty: DifficultyEasy, Need: 2000, Output: 512}
	if !EligibleForSwarm(small, rep.SwarmLight, solo, 0.5) {
		t.Error("small task = ineligible, want eligible")
	}
	hard := small
	hard.Difficulty = DifficultyHard
	if EligibleForSwarm(hard, rep.SwarmLight, solo, 0.5) {
		t.Error("hard task = eligible, want ineligible")
	}
	repo := small
	repo.Demand = DemandRepo
	if EligibleForSwarm(repo, rep.SwarmPods, solo, 0.5) {
		t.Error("repo task = eligible, want ineligible")
	}
	big := small
	big.Need = 20000
	if EligibleForSwarm(big, rep.SwarmLight, solo, 0.5) {
		t.Error("task above the relative ratio = eligible, want ineligible")
	}
}

func TestMissionReservesSoloForHardTask(t *testing.T) {
	rep := PlanMachine(m2Max())
	tasks := []TaskEstimate{
		{Capability: "code", Demand: DemandRepo, Difficulty: DifficultyHard, Need: 12000, Output: 4096},
		{Capability: "text", Demand: DemandShort, Difficulty: DifficultyEasy, Need: 2000, Output: 512},
		{Capability: "document", Demand: DemandShort, Difficulty: DifficultyEasy, Need: 2000, Output: 512},
	}
	mp := PlanMission(tasks, rep, 0.5)
	if len(mp.Lanes) == 0 {
		t.Fatal("no lanes")
	}
	if mp.Lanes[0].Name != "solo" {
		t.Errorf("first lane = %q, want solo", mp.Lanes[0].Name)
	}
	var light *Lane
	for i := range mp.Lanes {
		if mp.Lanes[i].Class == "light" {
			light = &mp.Lanes[i]
		}
	}
	if light == nil {
		t.Fatal("no light swarm lane")
	}
	if light.Slots != 2 {
		t.Errorf("light swarm slots = %d, want 2 (3 minus reserved solo)", light.Slots)
	}
}

func TestMissionTooLargeDisablesSwarm(t *testing.T) {
	rep := PlanMachine(m2Max())
	tasks := []TaskEstimate{
		{Capability: "text", Demand: DemandLong, Difficulty: DifficultyMedium, Need: 20000, Output: 512},
		{Capability: "text", Demand: DemandLong, Difficulty: DifficultyMedium, Need: 20000, Output: 512},
		{Capability: "text", Demand: DemandLong, Difficulty: DifficultyMedium, Need: 20000, Output: 512},
	}
	mp := PlanMission(tasks, rep, 0.5)
	if len(mp.Lanes) != 1 || mp.Lanes[0].Name != "solo" {
		t.Fatalf("lanes = %+v, want solo only", mp.Lanes)
	}
	if !hasMissionReason(mp, "mission_too_large_for_swarm") {
		t.Error("missing mission_too_large_for_swarm reason")
	}
}

func TestHeterogeneousIsConsidered(t *testing.T) {
	in := m2Max()
	// Make the small and tiny models the only viable swarm cast: both fit,
	// but force a context where the 3B cannot take many slots.
	in.Models = []ModelFit{
		model("a3b", "alpha", 2*gib, 96*1024, 8192, 0.70),
		model("b1b", "beta", 1*gib, 64*1024, 8192, 0.60),
	}
	in.MaxJobs = 2
	in.MaxLLMCalls = 2
	rep := PlanMachine(in)
	if rep.SwarmHeteroPods == nil {
		t.Fatal("SwarmHeteroPods = nil, want a heterogeneous plan")
	}
	if len(rep.SwarmHeteroPods.Groups) != 2 {
		t.Fatalf("groups = %+v, want two distinct models", rep.SwarmHeteroPods.Groups)
	}
	if rep.SwarmHeteroPods.Groups[0].Model == rep.SwarmHeteroPods.Groups[1].Model {
		t.Error("heterogeneous plan uses one model twice")
	}
	if rep.SwarmHeteroPods.Slots < 2 {
		t.Errorf("slots = %d, want >= 2", rep.SwarmHeteroPods.Slots)
	}
}

func TestLiteModeUsesLowerFloors(t *testing.T) {
	in := Inputs{
		HostBytes: 8 * gib, RuntimeBytes: 4 * gib, FreeDiskBytes: 40 * gib,
		Models: []ModelFit{model("only3b", "beta", 2*gib, 96*1024, 4096, 0.6)},
		Podman: true, JobPodImage: "pod", CoreReserveBytes: 1500 * mib,
		HostReserveBytes: 4 * gib, PerPodBytes: 1500 * mib,
		MaxJobs: 1, MaxLLMCalls: 1,
		Floors:         Floors{Capability: map[string]int{"code": 8192, "text": 8192}},
		DegradedPolicy: "pause",
	}
	rep := PlanMachine(in)
	if rep.Coverage.Capabilities["code"] {
		t.Error("code capability = true at 4k with the standard floor, want false")
	}
	in.DegradedPolicy = "lite"
	rep = PlanMachine(in)
	if !rep.Coverage.Capabilities["code"] {
		t.Error("code capability = false in lite mode at 4k, want true")
	}
	if !hasReason(rep, "lite_mode") {
		t.Error("missing lite_mode reason")
	}
}

func hasReason(rep Report, code string) bool {
	for _, r := range rep.Reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}

func hasMissionReason(mp MissionPlan, code string) bool {
	for _, r := range mp.Reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}

func TestSwarmRespectsContextCap(t *testing.T) {
	in := m2Max()
	in.SwarmContextCap = 8192
	rep := PlanMachine(in)
	if rep.SwarmPods == nil {
		t.Fatal("SwarmPods = nil")
	}
	for _, g := range rep.SwarmPods.Groups {
		if g.Context > 8192 {
			t.Errorf("group context = %d, want <= 8192 (SwarmContextCap)", g.Context)
		}
	}
}

func TestLightSwarmOnSmallMachine(t *testing.T) {
	in := Inputs{
		HostBytes: 8 * gib, RuntimeBytes: 4 * gib, FreeDiskBytes: 40 * gib,
		Models: []ModelFit{model("only3b", "beta", 2*gib, 96*1024, 8192, 0.6)},
		Podman: false, CoreReserveBytes: 1500 * mib, HostReserveBytes: 4 * gib,
		PerPodBytes: 1500 * mib, MaxJobs: 2, MaxLLMCalls: 2,
		SwarmContextCap: 8192,
	}
	rep := PlanMachine(in)
	if rep.SwarmPods != nil {
		t.Error("SwarmPods != nil without Podman, want nil")
	}
	if rep.SwarmLight == nil || rep.SwarmLight.Slots < 2 {
		t.Fatalf("SwarmLight = %+v, want a >=2-slot light swarm", rep.SwarmLight)
	}
	for _, g := range rep.SwarmLight.Groups {
		if g.Context > 4096 {
			t.Errorf("light context = %d, want <= 4096 on this budget", g.Context)
		}
	}
}

func TestMissionAllSmallUsesSwarmOnly(t *testing.T) {
	rep := PlanMachine(m2Max())
	tasks := []TaskEstimate{
		{Capability: "text", Demand: DemandShort, Difficulty: DifficultyEasy, Need: 2000, Output: 512},
		{Capability: "document", Demand: DemandShort, Difficulty: DifficultyEasy, Need: 2000, Output: 512},
	}
	mp := PlanMission(tasks, rep, 0.5)
	if len(mp.Lanes) == 0 {
		t.Fatal("no lanes")
	}
	for _, l := range mp.Lanes {
		if l.Name == "solo" {
			t.Errorf("lanes = %+v, want no solo reservation for an all-small mission", mp.Lanes)
		}
	}
}

func TestMissionReservesOneSlotTotal(t *testing.T) {
	rep := PlanMachine(m2Max())
	tasks := []TaskEstimate{
		{Capability: "code", Demand: DemandRepo, Difficulty: DifficultyHard, Need: 12000, Output: 4096},
		{Capability: "code", Demand: DemandShort, Difficulty: DifficultyEasy, Need: 2000, Output: 512},
		{Capability: "text", Demand: DemandShort, Difficulty: DifficultyEasy, Need: 2000, Output: 512},
	}
	mp := PlanMission(tasks, rep, 0.5)
	total := 0
	for _, l := range mp.Lanes {
		total += l.Slots
	}
	// m2Max has 3 pod slots + 3 light slots; one solo reservation removes
	// exactly one slot from one lane, not one from both.
	if total != 6 {
		t.Errorf("total slots = %d, want 6 (3+3, one moved to solo accounting)", total)
	}
}

func TestCoverageRequiresFitAtFloor(t *testing.T) {
	in := Inputs{
		HostBytes: 8 * gib, HostReserveBytes: 4 * gib, // 4 GiB inference budget
		Models:  []ModelFit{model("tooHeavy", "x", 4*gib, 96*1024, 32768, 0.8)},
		MaxJobs: 1, MaxLLMCalls: 1,
		Floors: Floors{Capability: map[string]int{"code": 8192, "text": 4096}},
	}
	rep := PlanMachine(in)
	if rep.Coverage.Capabilities["code"] {
		t.Error("code capability = true for a model that cannot fit at its floor, want false")
	}
	if rep.Coverage.Capabilities["text"] {
		t.Error("text capability = true for a model that cannot fit at its floor, want false")
	}
}
