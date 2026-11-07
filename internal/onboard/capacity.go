package onboard

import (
	"github.com/tcs76321/athanor/internal/capacity"
)

// CapacityOptions supplies the machine facts the model audit does not measure:
// the Podman runtime budget, Job Pod memory, operator job limits, and the
// configured embedding model.
type CapacityOptions struct {
	RuntimeBytes     int64
	CoreReserveBytes int64
	PerPodBytes      int64
	MaxJobs          int
	MaxLLMCalls      int
	Podman           bool
	JobPodImage      string
	EmbeddingModel   string
	EmbeddingBytes   int64
	FreeDiskBytes    int64
	RepoScratchBytes int64
}

// CapacityReport derives a capacity plan from a measured model audit. The audit
// measures each role's resident size at its context target; the derived
// KV/token is an estimate, and MaxContext is the measured target (conservative,
// not the architecture maximum).
func CapacityReport(a Audit, opts CapacityOptions) capacity.Report {
	byName := map[string]capacity.ModelFit{}
	for _, r := range a.Roles {
		if r.Model == "" || r.LoadedBytes <= 0 || r.ContextTarget <= 0 {
			continue
		}
		kv := int64(0)
		if r.LoadedBytes > r.FileBytes && r.FileBytes > 0 {
			kv = (r.LoadedBytes - r.FileBytes) / int64(r.ContextTarget)
		}
		// A missing KV estimate cannot be planned; a model with no KV number
		// is still useful for coverage, but the capacity package filters it.
		f := capacity.ModelFit{
			Name: r.Model, WeightsBytes: r.FileBytes, KVPerToken: kv,
			NativeContext: r.ContextTarget, MaxContext: r.ContextTarget, Score: 0.5,
		}
		if prev, ok := byName[r.Model]; ok && prev.WeightsBytes > f.WeightsBytes {
			f.WeightsBytes = prev.WeightsBytes
		}
		byName[r.Model] = f
	}
	models := make([]capacity.ModelFit, 0, len(byName))
	var maxWeights int64
	for _, m := range byName {
		if m.WeightsBytes > maxWeights {
			maxWeights = m.WeightsBytes
		}
	}
	for _, m := range byName {
		// The audit has no quality measurements yet. Use model size as a
		// transparent capability proxy: the largest measured model is the
		// solo preference, while swarm selection still prefers the cheapest
		// model that fits the slot.
		if maxWeights > 0 {
			m.Score = 0.5 + 0.4*float64(m.WeightsBytes)/float64(maxWeights)
		}
		models = append(models, m)
	}
	runtimeBytes := opts.RuntimeBytes
	if runtimeBytes <= 0 {
		// The audit does not measure the Podman VM. On Linux the runtime is
		// the host; on macOS this is an explicit over-estimate, so the report
		// labels it through the binding.
		runtimeBytes = a.HostBytes
	}
	return capacity.PlanMachine(capacity.Inputs{
		HostBytes: a.HostBytes, RuntimeBytes: runtimeBytes, FreeDiskBytes: opts.FreeDiskBytes,
		Models: models, EmbeddingModel: opts.EmbeddingModel, EmbeddingBytes: opts.EmbeddingBytes,
		Podman: opts.Podman, JobPodImage: opts.JobPodImage,
		CoreReserveBytes: opts.CoreReserveBytes, HostReserveBytes: a.ReserveBytes,
		PerPodBytes: opts.PerPodBytes, MaxJobs: opts.MaxJobs, MaxLLMCalls: opts.MaxLLMCalls,
		RepoScratchBytes: opts.RepoScratchBytes,
	})
}
