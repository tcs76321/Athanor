// Defaults for omitted optional configuration fields (§29 reference config).
package config

import "time"

const (
	minute = time.Minute
	second = time.Second
)

// Val resolves an optional boolean flag, returning def when unset.
func Val(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

// applyDefaults fills every omitted optional field with its documented
// default (§29 reference config).
func applyDefaults(c *Config) {
	setStr := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	setDur := func(dst *Duration, v Duration) {
		if *dst == 0 {
			*dst = v
		}
	}
	setInt := func(dst *int, v int) {
		if *dst == 0 {
			*dst = v
		}
	}
	setTrue := func(dst **bool) {
		if *dst == nil {
			v := true
			*dst = &v
		}
	}

	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	setStr(&c.Agent.ActiveHours, "00:00-24:00")
	setDur(&c.Agent.IdleThreshold, Duration(5*minute))
	setInt(&c.Agent.MaxProactiveTasksPerDay, 10)

	setTrue(&c.Power.RequireACForDeepWork)
	if c.Power.BatteryPauseThresholdPercent == 0 {
		c.Power.BatteryPauseThresholdPercent = 20
	}
	setDur(&c.Power.IdleResumeAfter, Duration(5*minute))
	setTrue(&c.Power.PauseOnSleep)
	setTrue(&c.Power.ResumeOnWake)
	setTrue(&c.Power.DaydreamOnIdle)
	setInt(&c.Power.DaydreamMaxConcurrent, 2)
	setInt(&c.Power.DaydreamMaxWallTimeMinutes, 30)

	setStr(&c.Inference.DefaultBackend, "ollama")
	setStr(&c.Inference.OllamaURL, "http://host.containers.internal:11434")
	setTrue(&c.Inference.CloudRequiresApproval)
	// M3-T7.1 (ADR-0012): grammar-constrain the structured judgment
	// phases by default; leave the sampler seed unseeded (production-
	// faithful) unless the operator opts into derived seeds.
	setTrue(&c.Inference.JSONFormat)
	setStr(&c.Inference.JudgmentSeed, JudgmentSeedOff)

	defaultPersona(&c.Personas.Wide, "qwen2.5:7b", 65536, 0.7)
	defaultPersona(&c.Personas.Tall, "qwen2.5-coder:32b", 16384, 0.2)
	defaultPersona(&c.Personas.Main, "mistral-nemo:12b", 32768, 0.4)
	defaultPersona(&c.Personas.Security, "phi3:3.8b", 8192, 0.0)
	defaultPersona(&c.Personas.Alternative, "llama3.1:8b", 32768, 0.8)

	setInt(&c.ContextEngine.CodingFloor, 32768)
	setInt(&c.ContextEngine.ResearchFloor, 32768)
	setInt(&c.ContextEngine.DocumentFloor, 16384)
	setInt(&c.ContextEngine.SimpleFloor, 8192)
	setTrue(&c.ContextEngine.EnableLosslessSwapping)
	if c.ContextEngine.KVCacheWarningThresh == 0 {
		c.ContextEngine.KVCacheWarningThresh = 0.85
	}
	if c.ContextEngine.KVCacheCriticalThresh == 0 {
		c.ContextEngine.KVCacheCriticalThresh = 0.95
	}
	// M5-T2 division bounds (§10.1). 1 MiB keeps the single-connection
	// SQLite blob store bounded; 128 KiB byte-splits any single oversized
	// declaration; 120 lines is the M5-T1 spike's fallback block size.
	if c.ContextEngine.DivisionMaxSourceBytes == 0 {
		c.ContextEngine.DivisionMaxSourceBytes = 1048576
	}
	if c.ContextEngine.DivisionMaxChunkBytes == 0 {
		c.ContextEngine.DivisionMaxChunkBytes = 131072
	}
	if c.ContextEngine.DivisionFallbackLines == 0 {
		c.ContextEngine.DivisionFallbackLines = 120
	}
	// M5-T7 retrieval (§10.2). Top-K defaults to 8. The embedding model has
	// no default: an empty name is the documented "full-text only" posture
	// (ADR-0026 §3), so the vector half stays inert until an operator opts
	// in by naming a model.
	if c.ContextEngine.MemorySearchTopK == 0 {
		c.ContextEngine.MemorySearchTopK = 8
	}
	// M5-T8 repository indexing (ADR-0028 §8): a pass is bounded by files
	// and model calls; the embedding digest defaults to a 2 KiB content
	// prefix, with an explicit 0 meaning path + summary only.
	if c.ContextEngine.IndexBatchFiles == 0 {
		c.ContextEngine.IndexBatchFiles = 50
	}
	if c.ContextEngine.IndexBatchChunks == 0 {
		c.ContextEngine.IndexBatchChunks = 200
	}
	if c.ContextEngine.IndexEmbedBytes == nil {
		v := 2048
		c.ContextEngine.IndexEmbedBytes = &v
	}

	setInt(&c.Execution.DivergenceCandidates, 3)
	setInt(&c.Execution.MaxHardTaskVariations, 10)
	// M3-T4 commit 4.2: the reflection-loop budget was a
	// hard-coded `maxReflectionIterations = 2` constant in
	// `internal/engine/reflect.go` since M3-T1. It is now
	// a config field; the engine reads it via
	// `cfg.Execution.MaxReflectionLoops` with a 2 default
	// to keep the M3-T1 behavior bit-identical when no
	// config is supplied.
	setInt(&c.Execution.MaxReflectionLoops, 2)
	setStr(&c.Execution.JudgePersona, "security")
	setTrue(&c.Execution.RequireTestsForCode)
	setTrue(&c.Execution.RequireDocumentationForCode)
	setTrue(&c.Execution.CompareBeforeAccept)
	// M6-T1 (ADR-0032): bounds for the decomposed task graph. They cap
	// the validator so one model call cannot queue an unbounded DAG.
	setInt(&c.Execution.DAGMaxTasks, 25)
	setInt(&c.Execution.DAGMaxDepth, 6)
	setInt(&c.Execution.DAGMaxTotalJobs, 100)
	// M6-T3 (ADR-0034): retries a failed task gets before it blocks and
	// escalates. Two retries (three attempts) is the documented default;
	// an explicit 0 is preserved (retries disabled).
	if c.Execution.MaxTaskRetries == nil {
		n := 2
		c.Execution.MaxTaskRetries = &n
	}
	if c.Execution.MinJudgeConfidence == nil {
		def := 0.7
		c.Execution.MinJudgeConfidence = &def
	}
	if c.Execution.PhaseWallTimeBudgets == nil {
		c.Execution.PhaseWallTimeBudgets = map[string]Duration{}
	}
	for phase, d := range map[string]Duration{
		"planning":   Duration(300 * second),
		"evaluating": Duration(600 * second),
		"default":    Duration(300 * second),
	} {
		if _, ok := c.Execution.PhaseWallTimeBudgets[phase]; !ok {
			c.Execution.PhaseWallTimeBudgets[phase] = d
		}
	}

	setTrue(&c.StrategyAnalysis.Enabled)
	setInt(&c.StrategyAnalysis.MinCohortSize, 20)
	if c.StrategyAnalysis.MinAcceptRateDelta == 0 {
		c.StrategyAnalysis.MinAcceptRateDelta = 0.15
	}
	setInt(&c.StrategyAnalysis.MaxActiveInsights, 10)
	setInt(&c.StrategyAnalysis.InsightExpiryDays, 90)
	setTrue(&c.StrategyAnalysis.StrategyNotesInPrompts)

	setInt(&c.Limits.MaxConcurrentJobs, 2)
	setInt(&c.Limits.MaxConcurrentLLMCalls, 1)
	setInt(&c.Limits.MaxConcurrentFetches, 5)
	setInt(&c.Limits.MaxTasksPerHour, 20)
	setInt(&c.Limits.MaxTotalRecoveriesPerJob, 8)

	setInt(&c.Recovery.MaxToolCallRetries, 2)
	setInt(&c.Recovery.MaxLoopInterventions, 1)
	setInt(&c.Recovery.MaxContextCompactions, 3)

	setStr(&c.Network.DefaultPolicy, "deny")
	setInt(&c.Network.RateLimitPerMinute, 30)
	if c.Network.MaxResponseBytes == 0 {
		c.Network.MaxResponseBytes = 10485760
	}
	setTrue(&c.Network.ReaderModeDefault)
	setTrue(&c.Network.BrowserModeRequiresApproval)
	// M3-T5/ADR-0011 follow-up: the external API
	// Host-header allowlist defaults to the §D1
	// loopback set. Empty entries are rejected by
	// `validateRaw` (config.go).
	if c.Network.ExternalAPIHostAllowlist == nil {
		c.Network.ExternalAPIHostAllowlist = []string{
			"127.0.0.1:7420",
			"localhost:7420",
			"[::1]:7420",
			"athanor.local:7420",
		}
	}

	setTrue(&c.Security.ScanIngressFiles)
	setTrue(&c.Security.ScanEgressFiles)
	setTrue(&c.Security.PromptInjectionScan)
	setTrue(&c.Security.QuarantineSuspiciousFiles)

	setTrue(&c.Backup.Auto)
	setStr(&c.Backup.Schedule, "0 3 * * *")
	setInt(&c.Backup.MaxLocalBackups, 10)
	setTrue(&c.Backup.IncludeWorkspaceMetadata)

	// M6-T4 (ADR-0035): HITL request lifetime and expiry sweep cadence.
	setDur(&c.HITL.DefaultTTL, Duration(24*time.Hour))
	setDur(&c.HITL.ExpiryInterval, Duration(minute))

	setStr(&c.Logging.Level, "info")
	if len(c.Logging.Categories) == 0 {
		c.Logging.Categories = append([]string(nil), Categories...)
	}

	// JobPod defaults (M2-T4). Image is left empty on purpose:
	// production must configure it explicitly so an operator cannot
	// accidentally launch a daemon that picks up the wrong base image.
	// Resource limits are zero so jobpod.withDefaults fills the §21.2
	// values (PidsLimit=64, MemoryMB=512, CPUs=1.0).
	if c.JobPod.DefaultTools == nil {
		c.JobPod.DefaultTools = []string{}
	}

	// Airlock defaults (M4-T2/T3/T4; ADR-0015). The pipeline
	// lists are the closed set shipped in M4-T3; a typo in
	// a user config is rejected at construction (the
	// registry's NewRegistry errors on unknown in-tree names).
	// Egress intentionally omits prompt-injection scanners
	// — LLM-generated data, scanning is a category error.
	// UserPrompt runs the heuristic only; size/zipbomb are
	// not meaningful on a string. The numeric thresholds
	// (100 MiB ingress, 100x decompression, 10k zip entries,
	// 2 KiB long-prompt threshold) are the §21.3 numbers in
	// the ROADMAP M4-T4 acceptance criterion; the
	// `prompt_injection_scan_long_user_prompts` default is
	// true (defense-by-default; the operator can opt out).
	setTrue(&c.Airlock.Enabled)
	if c.Airlock.Scanners.Ingress == nil {
		c.Airlock.Scanners.Ingress = []string{
			"prompt-injection-heuristic",
			"size",
			"zipbomb",
			"clamav",
			"yara",
		}
	}
	if c.Airlock.Scanners.Egress == nil {
		c.Airlock.Scanners.Egress = []string{
			"size",
			"zipbomb",
			"clamav",
			"yara",
		}
	}
	if c.Airlock.Scanners.UserPrompt == nil {
		c.Airlock.Scanners.UserPrompt = []string{
			"prompt-injection-heuristic",
		}
	}
	if c.Airlock.MaxIngressBytes == 0 {
		c.Airlock.MaxIngressBytes = 104857600 // 100 MiB
	}
	if c.Airlock.MaxUncompressedRatio == 0 {
		c.Airlock.MaxUncompressedRatio = 100
	}
	if c.Airlock.MaxZipEntries == 0 {
		c.Airlock.MaxZipEntries = 10000
	}
	if c.Airlock.PromptInjectionLongUserPromptThresholdBytes == 0 {
		c.Airlock.PromptInjectionLongUserPromptThresholdBytes = 2048
	}
	setTrue(&c.Airlock.PromptInjectionScanLongUserPrompts)
	// YaraRuleSet is left empty by default; the cmd
	// layer materializes the in-tree baseline ruleset
	// (scanner.DefaultYARARules) to <state-dir>/yara/.
	// Operators override by setting this field to a
	// private rule file's path; the cmd layer honors
	// the override verbatim. An empty string + an
	// absent binary = VerdictUncertain (fail-closed).
}

func defaultPersona(p *PersonaConfig, model string, ctx int, temp float64) {
	if p.Model == "" {
		p.Model = model
	}
	if p.ContextTarget == 0 {
		p.ContextTarget = ctx
	}
	if p.Temperature == nil {
		t := temp
		p.Temperature = &t
	}
}
