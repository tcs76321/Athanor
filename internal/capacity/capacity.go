// Package capacity plans how much model context and how many concurrent jobs
// a machine can actually support. It is the single source of truth behind
// max_concurrent_jobs, per-job context targets, homogeneous vs heterogeneous
// swarms, and degraded modes on machines that cannot cover every role.
//
// The package is pure: it takes measured machine/model facts and task demand
// estimates, and returns a plan. It performs no I/O, imports only the standard
// library, and is therefore exhaustively testable.
//
// Design rules:
//
//   - Inference (host RAM) and execution (Podman VM RAM) are separate budgets.
//   - Writes are not part of the plan; concurrency is bounded by measured
//     model residency first, then pod memory, disk, and operator limits.
//   - A swarm only accepts tasks smaller than the machine's solo capability.
//     "Smaller" is relative (SwarmTaskRatio) and absolute (demand/difficulty).
//   - Never silently reduce context below a floor. If the machine cannot meet
//     the floor, the capability is unavailable (or lite mode, when explicitly
//     enabled).
package capacity

import (
	"fmt"
	"sort"
	"strings"
)

// Demand describes how much context a task needs. It is inferred before the
// planner runs and may be refined by the planning model.
type Demand string

const (
	DemandShort  Demand = "short"
	DemandLong   Demand = "long"
	DemandRepo   Demand = "repo"
	DemandMemory Demand = "memory"
)

// Difficulty is the planner's easy/medium/hard hint.
type Difficulty string

const (
	DifficultyEasy   Difficulty = "easy"
	DifficultyMedium Difficulty = "medium"
	DifficultyHard   Difficulty = "hard"
)

// TaskEstimate is the planner's view of one task. Need and Output are token
// estimates; MinModelScore is a 0..1 capability threshold (0 means "any").
type TaskEstimate struct {
	Capability    string
	Demand        Demand
	Difficulty    Difficulty
	Need          int
	Output        int
	MinModelScore float64
}

// ModelFit is one measured model on this machine. KVPerToken is bytes per
// token of KV cache; MaxContext is the largest useful window the model can run
// at on this machine (already measured), not necessarily the architecture max.
type ModelFit struct {
	Name          string
	Family        string
	WeightsBytes  int64
	KVPerToken    int64
	NativeContext int
	MaxContext    int
	Score         float64
	TooLarge      bool
}

// Floors are the minimum windows by phase, demand, and capability. Lite is the
// explicitly opted-in degraded floor set.
type Floors struct {
	Planning   int
	Judge      int
	Demand     map[Demand]int
	Capability map[string]int
	Lite       map[string]int
}

// Inputs are the measured facts and operator policy for one machine.
type Inputs struct {
	HostBytes     int64 // physical host memory (Ollama lives here)
	RuntimeBytes  int64 // Podman VM memory (Core + Job Pods)
	FreeDiskBytes int64
	CPUs          int

	Models         []ModelFit
	EmbeddingModel string
	EmbeddingBytes int64
	JudgeModel     string
	JudgeBytes     int64
	Podman         bool
	JobPodImage    string
	// CoreReserveBytes is the Core Pod + DB + UI footprint inside the VM.
	CoreReserveBytes int64
	// HostReserveBytes is OS/apps/headroom outside Ollama.
	HostReserveBytes int64
	// PerPodBytes is the memory a Job Pod reserves (adaptive by capability;
	// callers pass the code-task value).
	PerPodBytes int64
	// PerSlotOverheadBytes covers allocator fragmentation, tokenizers, and
	// per-request buffers on the host.
	PerSlotOverheadBytes int64
	// RepoScratchBytes is the disk one code job needs for a repo snapshot.
	RepoScratchBytes int64

	MaxJobs     int
	MaxLLMCalls int

	// HeadroomPercent is the aggregate usage reserve applied to host and VM
	// budgets before slots are counted.
	HeadroomPercent int
	// SwarmTaskRatio is the largest fraction of the solo context a swarm task
	// may need. 0.5 means "swarm tasks must be at most half the solo window".
	SwarmTaskRatio float64
	// SwarmContextCap is the largest num_ctx a swarm slot may use. Swarms are
	// for smaller tasks; keeping this bounded makes KV cost predictable and
	// leaves the deep window to the solo lane.
	SwarmContextCap int
	// Buckets are the allowed num_ctx values, ascending.
	Buckets []int
	Floors  Floors

	// DegradedPolicy is "pause" (default) or "lite" (explicit opt-in).
	DegradedPolicy string
	// MinCapabilityScore is the default model score a capability requires.
	MinCapabilityScore float64
}

// Slot is one model/context assignment.
type Slot struct {
	Model         string
	Family        string
	Context       int
	Bucket        int
	ResidentBytes int64
	PodBytes      int64
	Binding       string
}

// Group is one model group inside a swarm. Slots is how many concurrent jobs
// may use this model at this context.
type Group struct {
	Model         string
	Family        string
	Context       int
	Bucket        int
	Slots         int
	ResidentBytes int64
	Score         float64
}

// Swarm is a multi-slot plan. Class is "pods" (code execution) or "light"
// (text/document/data, no Job Pod).
type Swarm struct {
	Class         string
	Groups        []Group
	Slots         int
	Context       int // conservative (minimum) group context
	MaxModelScore float64
	MinModelScore float64
	HeadroomBytes int64
	Binding       string
}

// Coverage is the honest picture of what this machine can specialize.
type Coverage struct {
	Models           int
	Families         int
	SingleModel      bool
	Roles            map[string]bool
	MissingRoles     []string
	Embedding        bool
	VectorMemory     bool
	CodeExecution    bool
	CrossFamilyJudge bool
	Capabilities     map[string]bool
}

// Reason is one planner explanation. Severity is "info", "warn", or "error".
type Reason struct {
	Code     string
	Severity string
	Detail   string
}

// Report is the complete machine plan.
type Report struct {
	HostBytes    int64
	RuntimeBytes int64
	HostBudget   int64
	VMBudget     int64
	FixedBytes   int64

	Solo             *Slot
	SwarmPods        *Swarm
	SwarmLight       *Swarm
	SwarmHeteroPods  *Swarm
	SwarmHeteroLight *Swarm

	Coverage Coverage
	Reasons  []Reason
}

// Lane is one mission scheduling lane.
type Lane struct {
	Name  string // "solo" or "swarm"
	Class string // "pods" or "light" ("" for solo)
	Slots int
	Plan  Slot   // solo plan
	Swarm *Swarm // swarm plan (slots adjusted for this lane)
}

// MissionPlan partitions a mission's tasks across lanes. Solo is reserved for
// tasks the swarm cannot honestly run.
type MissionPlan struct {
	Lanes   []Lane
	Reasons []Reason
}

const (
	defaultHeadroomPercent = 20
	defaultSwarmTaskRatio  = 0.5
	defaultSlotOverhead    = int64(64 << 20) // 64 MiB
	defaultMinCapability   = 0.5
	defaultLiteCapability  = "text"
)

// PlanMachine computes the solo plan, the pod and light swarm plans, coverage,
// and degraded-mode reasons for one machine.
func PlanMachine(in Inputs) Report {
	in = normalize(in)
	rep := Report{
		HostBytes: in.HostBytes, RuntimeBytes: in.RuntimeBytes,
		HostBudget: max64(0, in.HostBytes-in.HostReserveBytes),
		VMBudget:   max64(0, in.RuntimeBytes-in.CoreReserveBytes),
	}
	fixed := in.EmbeddingBytes
	if in.JudgeModel != "" && in.JudgeModel != in.EmbeddingModel {
		fixed += in.JudgeBytes
	}
	rep.FixedBytes = fixed

	eligible := eligibleModels(in.Models)
	rep.Coverage = coverageOf(in, eligible, fixed)
	rep.Reasons = append(rep.Reasons, coverageReasons(rep.Coverage)...)

	// Solo: the best model that fits one job, preferring quality. If a pod
	// does not fit the VM, fall back to a podless solo plan (text work still
	// runs) and record why code execution is unavailable.
	rep.Solo = planSolo(in, eligible, fixed, true, rep.HostBudget, rep.VMBudget)
	if rep.Solo == nil {
		rep.Solo = planSolo(in, eligible, fixed, false, rep.HostBudget, rep.VMBudget)
		if rep.Solo != nil {
			rep.Reasons = append(rep.Reasons, Reason{
				Code: "solo_without_pod", Severity: "warn",
				Detail: "no Job Pod fits the runtime budget; solo work is limited to capabilities that do not execute code",
			})
		}
	}
	if rep.Solo == nil {
		rep.Reasons = append(rep.Reasons, Reason{
			Code: "no_model", Severity: "error",
			Detail: "no installed model fits the host inference budget; install a smaller or more quantized model",
		})
		return rep
	}

	// Swarm plans. The pod plan requires a code-capable runtime.
	if in.Podman && in.JobPodImage != "" {
		rep.SwarmPods, rep.SwarmHeteroPods = planSwarm(in, eligible, fixed, "pods", in.PerPodBytes, rep.HostBudget, rep.VMBudget)
	}
	rep.SwarmLight, rep.SwarmHeteroLight = planSwarm(in, eligible, fixed, "light", 0, rep.HostBudget, rep.VMBudget)

	if rep.SwarmPods == nil && rep.SwarmLight == nil {
		rep.Reasons = append(rep.Reasons, Reason{
			Code: "no_swarm", Severity: "info",
			Detail: "no two-slot swarm fits the measured host/VM budgets at a useful context; work runs solo",
		})
	}
	if rep.SwarmPods == nil && in.Podman {
		rep.Reasons = append(rep.Reasons, Reason{
			Code: "no_pod_swarm", Severity: "info",
			Detail: "code swarms do not fit; text/document/data may still swarm without a Job Pod",
		})
	}
	if !rep.Coverage.CodeExecution {
		rep.Reasons = append(rep.Reasons, Reason{
			Code: "no_code_execution", Severity: "warn",
			Detail: "no working Job Pod runtime; code tasks are blocked, text/document/data remain available",
		})
	}
	if in.DegradedPolicy == "lite" {
		rep.Reasons = append(rep.Reasons, Reason{
			Code: "lite_mode", Severity: "warn",
			Detail: "lite context floors are enabled; quality may be lower than the standard floors",
		})
	}
	return rep
}

// EligibleForSwarm applies the relative and absolute rules: only tasks smaller
// than the machine's solo capability, and only tasks whose need fits a group.
func EligibleForSwarm(t TaskEstimate, s *Swarm, solo Slot, ratio float64) bool {
	if s == nil || s.Slots < 2 {
		return false
	}
	if t.Demand == DemandRepo || t.Difficulty == DifficultyHard {
		return false
	}
	if solo.Context <= 0 {
		return false
	}
	if ratio <= 0 {
		ratio = defaultSwarmTaskRatio
	}
	if t.Need > int(solo.Context) {
		return false
	}
	// Relative rule: swarm tasks must be smaller than the machine's solo
	// capability. A task that needs the whole solo window belongs in solo.
	if float64(t.Need) > float64(solo.Context)*ratio {
		return false
	}
	if t.Need+t.Output > s.Context {
		return false
	}
	if t.MinModelScore > s.MaxModelScore {
		return false
	}
	return true
}

// PlanMission decides which lanes a mission uses. It reserves the solo lane
// when any task is ineligible for a swarm, and disables swarming entirely when
// the mission's 75th-percentile task is too large for the relative rule.
func PlanMission(tasks []TaskEstimate, rep Report, ratio float64) MissionPlan {
	if ratio <= 0 {
		ratio = defaultSwarmTaskRatio
	}
	mp := MissionPlan{}
	if rep.Solo == nil {
		mp.Reasons = append(mp.Reasons, Reason{
			Code: "no_model", Severity: "error", Detail: "no solo model fits; cannot schedule work",
		})
		return mp
	}
	if len(tasks) == 0 {
		mp.Lanes = append(mp.Lanes, Lane{Name: "solo", Slots: 1, Plan: *rep.Solo})
		return mp
	}
	needs := make([]int, 0, len(tasks))
	for _, t := range tasks {
		needs = append(needs, t.Need+t.Output)
	}
	if percentile(needs, 75) > int(float64(rep.Solo.Context)*ratio) {
		mp.Reasons = append(mp.Reasons, Reason{
			Code: "mission_too_large_for_swarm", Severity: "info",
			Detail: "the mission's 75th-percentile task is too large for the relative swarm rule; running solo",
		})
		mp.Lanes = append(mp.Lanes, Lane{Name: "solo", Slots: 1, Plan: *rep.Solo})
		return mp
	}

	pods := rep.SwarmPods
	light := rep.SwarmLight
	podsEligible, podsIneligible := classify(tasks, "pods", pods, *rep.Solo, ratio)
	lightEligible, lightIneligible := classify(tasks, "light", light, *rep.Solo, ratio)

	needSolo := podsIneligible+lightIneligible > 0
	// A task class with no eligible tasks does not get a swarm lane.
	if podsEligible == 0 {
		pods = nil
	}
	if lightEligible == 0 {
		light = nil
	}
	if pods == nil && light == nil {
		mp.Lanes = append(mp.Lanes, Lane{Name: "solo", Slots: 1, Plan: *rep.Solo})
		return mp
	}

	if needSolo {
		mp.Lanes = append(mp.Lanes, Lane{Name: "solo", Slots: 1, Plan: *rep.Solo})
		// Reserve one slot total, not one from every lane.
		if pods != nil && (light == nil || pods.Slots >= light.Slots) {
			pods = shrinkSwarm(pods)
		} else {
			light = shrinkSwarm(light)
		}
	}
	if pods != nil && pods.Slots >= 2 {
		mp.Lanes = append(mp.Lanes, Lane{Name: "swarm", Class: "pods", Slots: pods.Slots, Swarm: pods})
	}
	if light != nil && light.Slots >= 2 {
		mp.Lanes = append(mp.Lanes, Lane{Name: "swarm", Class: "light", Slots: light.Slots, Swarm: light})
	}
	if len(mp.Lanes) == 0 {
		mp.Lanes = append(mp.Lanes, Lane{Name: "solo", Slots: 1, Plan: *rep.Solo})
	}
	return mp
}

// classify counts eligible and ineligible tasks for one swarm class. A task
// whose class has no swarm plan at all is ineligible: it needs the solo lane.
func classify(tasks []TaskEstimate, class string, s *Swarm, solo Slot, ratio float64) (eligible, ineligible int) {
	for _, t := range tasks {
		if classOf(t.Capability) != class {
			continue
		}
		if EligibleForSwarm(t, s, solo, ratio) {
			eligible++
		} else {
			ineligible++
		}
	}
	return eligible, ineligible
}

// shrinkSwarm removes one slot to make room for the reserved solo lane.
func shrinkSwarm(s *Swarm) *Swarm {
	if s == nil || s.Slots < 2 {
		return nil
	}
	cp := *s
	cp.Groups = append([]Group(nil), s.Groups...)
	cp.Slots--
	// Remove one slot from the largest group; drop empty groups.
	best := 0
	for i := range cp.Groups {
		if cp.Groups[i].Slots > cp.Groups[best].Slots {
			best = i
		}
	}
	cp.Groups[best].Slots--
	kept := cp.Groups[:0]
	for _, g := range cp.Groups {
		if g.Slots > 0 {
			kept = append(kept, g)
		}
	}
	cp.Groups = kept
	if cp.Slots < 2 || len(cp.Groups) == 0 {
		return nil
	}
	return &cp
}

// classOf maps a capability to its swarm class.
func classOf(capability string) string {
	if capability == "code" {
		return "pods"
	}
	return "light"
}

// planSolo selects the best-fitting single slot.
func planSolo(in Inputs, eligible []ModelFit, fixed int64, withPod bool, hostBudget, vmBudget int64) *Slot {
	pod := int64(0)
	if withPod {
		pod = in.PerPodBytes
	}
	if withPod && (vmBudget <= 0 || pod <= 0 || pod+in.CoreReserveBytes > in.RuntimeBytes) {
		return nil
	}
	models := append([]ModelFit(nil), eligible...)
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].Score != models[j].Score {
			return models[i].Score > models[j].Score
		}
		if models[i].WeightsBytes != models[j].WeightsBytes {
			return models[i].WeightsBytes < models[j].WeightsBytes
		}
		return models[i].Name < models[j].Name
	})
	for _, m := range models {
		// Try the largest useful bucket first; step down until the model fits.
		// This lets a stronger model run at 16k when 32k would not fit.
		for _, bucket := range reverseInts(usableBuckets(in.Buckets, m.MaxContext)) {
			resident := residentBytes(m, bucket)
			if resident+fixed > hostBudget {
				continue
			}
			if withPod && pod+in.CoreReserveBytes > in.RuntimeBytes {
				continue
			}
			return &Slot{
				Model: m.Name, Family: m.Family, Context: bucket, Bucket: bucket,
				ResidentBytes: resident, PodBytes: pod, Binding: "best measured model that fits solo",
			}
		}
	}
	return nil
}

// planSwarm finds the best homogeneous or heterogeneous two-or-more slot plan.
func planSwarm(in Inputs, eligible []ModelFit, fixed int64, class string, perPod int64, hostBudget, vmBudget int64) (*Swarm, *Swarm) {
	if len(eligible) == 0 {
		return nil, nil
	}
	if class == "pods" {
		if vmBudget <= 0 || perPod <= 0 {
			return nil, nil
		}
		if perPod+in.CoreReserveBytes > in.RuntimeBytes {
			return nil, nil
		}
	}
	pool := eligible
	if filtered := filterScore(eligible, in.MinCapabilityScore); len(filtered) > 0 {
		pool = filtered
	}
	homo := planHomogeneous(in, pool, fixed, class, perPod, hostBudget, vmBudget)
	hetero := planHeterogeneous(in, pool, fixed, class, perPod, hostBudget, vmBudget)
	return betterSwarm(homo, hetero), hetero
}

// planHomogeneous finds the best N-copies-of-one-model plan. Weights are
// resident once; KV and per-request buffers are counted per slot.
func planHomogeneous(in Inputs, eligible []ModelFit, fixed int64, class string, perPod int64, hostBudget, vmBudget int64) *Swarm {
	var best *Swarm
	for _, m := range eligible {
		for _, bucket := range usableBuckets(in.Buckets, minInt(m.MaxContext, in.SwarmContextCap)) {
			resident := residentBytes(m, bucket)
			availHost := headroom(hostBudget-fixed-resident, in.HeadroomPercent)
			if availHost <= 0 {
				continue
			}
			perSeq := m.KVPerToken*int64(bucket) + in.PerSlotOverheadBytes
			if perSeq <= 0 {
				continue
			}
			nHost := int(availHost / perSeq)
			nVM := in.MaxJobs
			if class == "pods" {
				nVM = int(headroom(vmBudget-in.CoreReserveBytes, in.HeadroomPercent) / perPod)
			}
			n := minInt(nHost, nVM)
			n = minInt(n, in.MaxJobs)
			n = minInt(n, in.MaxLLMCalls)
			n = minInt(n, diskSlots(in, n))
			if n < 2 {
				continue
			}
			total := resident + int64(n)*perSeq
			cand := &Swarm{
				Class: class, Slots: n, Context: bucket,
				MaxModelScore: m.Score, MinModelScore: m.Score,
				Groups: []Group{{
					Model: m.Name, Family: m.Family, Context: bucket, Bucket: bucket,
					Slots: n, ResidentBytes: total, Score: m.Score,
				}},
				HeadroomBytes: max64(0, hostBudget-fixed-total),
				Binding:       fmt.Sprintf("homogeneous: weights once + %d KV sequences", n),
			}
			best = betterSwarm(best, cand)
		}
	}
	return best
}

// planHeterogeneous finds the best two-family plan. Both models stay resident;
// each serves the same number of slots (the conservative symmetric form).
func planHeterogeneous(in Inputs, eligible []ModelFit, fixed int64, class string, perPod int64, hostBudget, vmBudget int64) *Swarm {
	var best *Swarm
	for i := 0; i < len(eligible); i++ {
		for j := i + 1; j < len(eligible); j++ {
			a, b := eligible[i], eligible[j]
			if a.Family == b.Family || a.Family == "" || b.Family == "" {
				continue
			}
			for _, bucket := range usableBuckets(in.Buckets, minInt(minInt(a.MaxContext, b.MaxContext), in.SwarmContextCap)) {
				resident := residentBytes(a, bucket) + residentBytes(b, bucket)
				availHost := headroom(hostBudget-fixed-resident, in.HeadroomPercent)
				if availHost <= 0 {
					continue
				}
				perA := a.KVPerToken*int64(bucket) + in.PerSlotOverheadBytes
				perB := b.KVPerToken*int64(bucket) + in.PerSlotOverheadBytes
				if perA <= 0 || perB <= 0 {
					continue
				}
				n := minInt(int(availHost/perA), int(availHost/perB))
				if class == "pods" {
					n = minInt(n, int(headroom(vmBudget-in.CoreReserveBytes, in.HeadroomPercent)/(2*perPod)))
				}
				n = minInt(n, in.MaxJobs/2)
				n = minInt(n, in.MaxLLMCalls/2)
				n = minInt(n, diskSlots(in, 2*n)/2)
				if n < 1 {
					continue
				}
				total := resident + int64(n)*(perA+perB)
				cand := &Swarm{
					Class: class, Slots: 2 * n, Context: bucket,
					MaxModelScore: maxFloat(a.Score, b.Score), MinModelScore: minFloat(a.Score, b.Score),
					Groups: []Group{
						{Model: a.Name, Family: a.Family, Context: bucket, Bucket: bucket, Slots: n, ResidentBytes: residentBytes(a, bucket) + int64(n)*perA, Score: a.Score},
						{Model: b.Name, Family: b.Family, Context: bucket, Bucket: bucket, Slots: n, ResidentBytes: residentBytes(b, bucket) + int64(n)*perB, Score: b.Score},
					},
					HeadroomBytes: max64(0, hostBudget-fixed-total),
					Binding:       "heterogeneous: distinct families, equal slots",
				}
				best = betterSwarm(best, cand)
			}
		}
	}
	return best
}

// betterSwarm prefers throughput first, then the cheaper resident footprint,
// then the stronger model, then the larger context.
func betterSwarm(a, b *Swarm) *Swarm {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.Slots != b.Slots {
		if a.Slots > b.Slots {
			return a
		}
		return b
	}
	if a.Context != b.Context {
		if a.Context > b.Context {
			return a
		}
		return b
	}
	ra, rb := swarmResident(a), swarmResident(b)
	if ra != rb {
		if ra < rb {
			return a
		}
		return b
	}
	sa, sb := avgScore(a.Groups), avgScore(b.Groups)
	if sa != sb {
		if sa > sb {
			return a
		}
		return b
	}
	return a
}

func filterScore(models []ModelFit, min float64) []ModelFit {
	out := make([]ModelFit, 0, len(models))
	for _, m := range models {
		if m.Score >= min {
			out = append(out, m)
		}
	}
	return out
}

func swarmResident(s *Swarm) int64 {
	var total int64
	for _, g := range s.Groups {
		total += g.ResidentBytes
	}
	return total
}

func coverageOf(in Inputs, eligible []ModelFit, fixed int64) Coverage {
	fams := map[string]bool{}
	for _, m := range eligible {
		if m.Family != "" {
			fams[m.Family] = true
		}
	}
	c := Coverage{
		Models: len(eligible), Families: len(fams), SingleModel: len(eligible) == 1,
		Embedding:        in.EmbeddingModel != "" && in.EmbeddingBytes > 0,
		CrossFamilyJudge: len(fams) >= 2,
		CodeExecution:    in.Podman && in.JobPodImage != "",
		Roles:            map[string]bool{}, Capabilities: map[string]bool{},
	}
	c.VectorMemory = c.Embedding
	c.Roles["main"] = len(eligible) >= 1
	c.Roles["security"] = len(eligible) >= 1
	c.Roles["wide"] = len(eligible) >= 2
	c.Roles["tall"] = len(eligible) >= 2
	c.Roles["alternative"] = len(eligible) >= 2
	for _, role := range []string{"wide", "tall", "main", "security", "alternative"} {
		if !c.Roles[role] {
			c.MissingRoles = append(c.MissingRoles, role)
		}
	}
	// Capabilities are available only when a model can meet the floor *and*
	// fit the host budget at that floor, and — for code — when a Job Pod fits
	// the runtime. Lite floors are used only when the operator opted in.
	floors := in.Floors.Capability
	if in.DegradedPolicy == "lite" {
		floors = in.Floors.Lite
	}
	podFits := in.Podman && in.JobPodImage != "" &&
		in.PerPodBytes > 0 && in.PerPodBytes+in.CoreReserveBytes <= in.RuntimeBytes
	c.CodeExecution = podFits
	hostBudget := max64(0, in.HostBytes-in.HostReserveBytes)
	for _, cap := range []string{"code", "data", "document", "text", "media"} {
		floor := floors[cap]
		if floor <= 0 {
			floor = in.Floors.Capability[cap]
		}
		c.Capabilities[cap] = fitsAt(eligible, floor, fixed, hostBudget) &&
			(cap != "code" || podFits)
	}
	return c
}

func fitsAt(models []ModelFit, floor int, fixed, hostBudget int64) bool {
	if floor <= 0 {
		return false
	}
	for _, m := range models {
		if m.MaxContext < floor {
			continue
		}
		if residentBytes(m, floor)+fixed <= hostBudget {
			return true
		}
	}
	return false
}

func coverageReasons(c Coverage) []Reason {
	var out []Reason
	if c.SingleModel {
		out = append(out, Reason{
			Code: "single_model", Severity: "info",
			Detail: "one model is available: roles fall back to it, casting is homogeneous, and judgment is same-family unless another model is added",
		})
	}
	if len(c.MissingRoles) > 0 {
		out = append(out, Reason{
			Code: "missing_roles", Severity: "info",
			Detail: "missing specialized roles: " + strings.Join(c.MissingRoles, ", ") + "; each falls back to main",
		})
	}
	if !c.Embedding {
		out = append(out, Reason{
			Code: "no_embedding", Severity: "warn",
			Detail: "no embedding model: memory retrieval is full-text only (vector memory disabled)",
		})
	}
	if !c.CrossFamilyJudge {
		out = append(out, Reason{
			Code: "same_family_judge", Severity: "warn",
			Detail: "no second model family: judgment is same-family and therefore advisory only",
		})
	}
	return out
}

func normalize(in Inputs) Inputs {
	if in.Buckets == nil {
		in.Buckets = []int{4096, 8192, 16384, 32768}
	}
	if in.HeadroomPercent <= 0 || in.HeadroomPercent > 80 {
		in.HeadroomPercent = defaultHeadroomPercent
	}
	if in.SwarmTaskRatio <= 0 || in.SwarmTaskRatio > 1 {
		in.SwarmTaskRatio = defaultSwarmTaskRatio
	}
	if in.SwarmContextCap <= 0 {
		in.SwarmContextCap = 8192
	}
	if in.PerSlotOverheadBytes <= 0 {
		in.PerSlotOverheadBytes = defaultSlotOverhead
	}
	if in.MaxJobs < 1 {
		in.MaxJobs = 1
	}
	if in.MaxLLMCalls < 1 {
		in.MaxLLMCalls = 1
	}
	if in.MinCapabilityScore <= 0 {
		in.MinCapabilityScore = defaultMinCapability
	}
	if in.Floors.Planning <= 0 {
		in.Floors.Planning = 8192
	}
	if in.Floors.Judge <= 0 {
		in.Floors.Judge = 8192
	}
	if in.Floors.Capability == nil {
		in.Floors.Capability = map[string]int{
			"code": 8192, "data": 4096, "document": 8192, "text": 4096, "media": 8192,
		}
	}
	if in.Floors.Demand == nil {
		in.Floors.Demand = map[Demand]int{
			DemandShort: 4096, DemandLong: 16384, DemandRepo: 32768, DemandMemory: 16384,
		}
	}
	if in.Floors.Lite == nil {
		in.Floors.Lite = map[string]int{
			"code": 4096, "data": 2048, "document": 4096, "text": 2048, "media": 4096,
		}
	}
	return in
}

// reverseInts returns a copy in descending order.
func reverseInts(in []int) []int {
	out := append([]int(nil), in...)
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out
}

func eligibleModels(models []ModelFit) []ModelFit {
	out := make([]ModelFit, 0, len(models))
	for _, m := range models {
		if m.TooLarge || m.WeightsBytes <= 0 || m.KVPerToken <= 0 || m.MaxContext <= 0 {
			continue
		}
		if m.Score <= 0 {
			m.Score = 0.5
		}
		out = append(out, m)
	}
	return out
}

func residentBytes(m ModelFit, ctx int) int64 {
	return m.WeightsBytes + m.KVPerToken*int64(ctx)
}

func usableBuckets(buckets []int, maxContext int) []int {
	var out []int
	for _, b := range buckets {
		if b > 0 && b <= maxContext {
			out = append(out, b)
		}
	}
	return out
}

func headroom(bytes int64, percent int) int64 {
	if bytes <= 0 {
		return 0
	}
	return bytes * int64(100-percent) / 100
}

func diskSlots(in Inputs, proposed int) int {
	if in.RepoScratchBytes <= 0 || in.FreeDiskBytes <= 0 {
		return proposed
	}
	// Leave at least half the free disk for the DB, WAL, and archives.
	usable := in.FreeDiskBytes / 2
	n := int(usable / in.RepoScratchBytes)
	if n < 1 {
		return 1
	}
	return minInt(proposed, n)
}

func avgScore(groups []Group) float64 {
	if len(groups) == 0 {
		return 0
	}
	sum := 0.0
	for _, g := range groups {
		sum += g.Score
	}
	return sum / float64(len(groups))
}

func percentile(values []int, p int) int {
	if len(values) == 0 {
		return 0
	}
	cp := append([]int(nil), values...)
	sort.Ints(cp)
	idx := (len(cp) - 1) * p / 100
	return cp[idx]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
