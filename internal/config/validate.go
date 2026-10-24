// Semantic validation for the Athanor configuration (ARCHITECTURE §29).
//
// Two phases:
//   - validateRaw runs on user-provided values BEFORE defaults resolution,
//     so explicitly invalid values are rejected instead of silently replaced.
//     Zero values are treated as "not provided" and skipped.
//   - validateCross runs AFTER defaults and checks cross-field constraints.
package config

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// validateRaw rejects explicitly invalid values before defaults are applied.
func validateRaw(c *Config) error {
	if c.Version != 0 && c.Version != CurrentVersion {
		return fmt.Errorf("unsupported config version %d (want %d)", c.Version, CurrentVersion)
	}
	if c.Agent.ActiveHours != "" {
		if err := checkActiveHours(c.Agent.ActiveHours); err != nil {
			return fmt.Errorf("agent.active_hours: %w", err)
		}
	}
	if t := c.Power.BatteryPauseThresholdPercent; t != 0 && (t < 0 || t > 100) {
		return fmt.Errorf("power.battery_pause_threshold_percent must be 0-100, got %d", t)
	}
	if b := c.Inference.DefaultBackend; b != "" && b != "ollama" && b != "cloud" {
		return fmt.Errorf("inference.default_backend must be \"ollama\" or \"cloud\", got %q", b)
	}
	switch s := c.Inference.JudgmentSeed; s {
	case "", JudgmentSeedOff, JudgmentSeedDerived:
	default:
		return fmt.Errorf("inference.judgment_seed must be %q or %q, got %q",
			JudgmentSeedOff, JudgmentSeedDerived, s)
	}
	if c.Inference.MaxOutputTokens < 0 {
		return fmt.Errorf("inference.max_output_tokens must be >= 0, got %d", c.Inference.MaxOutputTokens)
	}
	if k := c.Inference.KeepAlive; k != "" {
		if _, err := time.ParseDuration(k); err != nil {
			return fmt.Errorf("inference.keep_alive must be a duration like \"5m\" (or empty), got %q", k)
		}
	}
	for _, p := range []struct {
		name string
		cfg  PersonaConfig
	}{
		{"wide", c.Personas.Wide},
		{"tall", c.Personas.Tall},
		{"main", c.Personas.Main},
		{"security", c.Personas.Security},
		{"alternative", c.Personas.Alternative},
	} {
		if p.cfg.Model != "" || p.cfg.ContextTarget != 0 || p.cfg.Temperature != nil {
			if p.cfg.Model == "" {
				return fmt.Errorf("personas.%s.model must not be empty", p.name)
			}
			if p.cfg.ContextTarget != 0 && p.cfg.ContextTarget <= 0 {
				return fmt.Errorf("personas.%s.context_target must be positive", p.name)
			}
			if p.cfg.Temperature != nil && (*p.cfg.Temperature < 0 || *p.cfg.Temperature > 2) {
				return fmt.Errorf("personas.%s.temperature must be within [0, 2], got %v", p.name, *p.cfg.Temperature)
			}
		}
	}
	floors := map[string]int{
		"context_engine.coding_floor":   c.ContextEngine.CodingFloor,
		"context_engine.research_floor": c.ContextEngine.ResearchFloor,
		"context_engine.document_floor": c.ContextEngine.DocumentFloor,
		"context_engine.simple_floor":   c.ContextEngine.SimpleFloor,
	}
	for name, v := range floors {
		if v != 0 && v <= 0 {
			return fmt.Errorf("%s must be positive, got %d", name, v)
		}
	}
	// M5-T2 division bounds (§10.1): reject explicitly negative values
	// before defaults run, so a typo surfaces with an actionable error
	// instead of silently becoming the default through applyDefaults.
	if v := c.ContextEngine.DivisionMaxSourceBytes; v < 0 {
		return fmt.Errorf("context_engine.division_max_source_bytes must be positive, got %d", v)
	}
	if v := c.ContextEngine.DivisionMaxChunkBytes; v < 0 {
		return fmt.Errorf("context_engine.division_max_chunk_bytes must be positive, got %d", v)
	}
	if v := c.ContextEngine.DivisionFallbackLines; v < 0 {
		return fmt.Errorf("context_engine.division_fallback_lines must be positive, got %d", v)
	}
	// M5-T7 retrieval: a negative top-k is a typo; 0 is indistinguishable
	// from unset and is replaced by the default in applyDefaults.
	if v := c.ContextEngine.MemorySearchTopK; v < 0 {
		return fmt.Errorf("context_engine.memory_search_top_k must be positive, got %d", v)
	}
	// M5-T8 repository indexing: a negative batch or digest size is a typo.
	// An explicit index_embed_bytes of 0 is valid (path + summary only).
	if v := c.ContextEngine.IndexBatchFiles; v < 0 {
		return fmt.Errorf("context_engine.index_batch_files must be positive, got %d", v)
	}
	if v := c.ContextEngine.IndexBatchChunks; v < 0 {
		return fmt.Errorf("context_engine.index_batch_chunks must be positive, got %d", v)
	}
	if v := c.ContextEngine.IndexEmbedBytes; v != nil && *v < 0 {
		return fmt.Errorf("context_engine.index_embed_bytes must be >= 0, got %d", *v)
	}
	// F5 automatic working set (ADR-0059): negative bounds are typos; 0 is
	// replaced by the default in applyDefaults.
	if v := c.ContextEngine.RepositoryIndexLimit; v < 0 {
		return fmt.Errorf("context_engine.repository_index_limit must be positive, got %d", v)
	}
	if v := c.ContextEngine.SeedActiveMaxBytes; v < 0 {
		return fmt.Errorf("context_engine.seed_active_max_bytes must be positive, got %d", v)
	}
	// Invariant §4.3: all compaction runs at Temp 0.0. Enforced here so no
	// configuration can weaken it.
	if t := c.ContextEngine.CompactionTemperature; t != 0 {
		return fmt.Errorf("context_engine.compaction_temperature must be 0.0 (invariant: all compaction at Temp 0.0), got %v", t)
	}
	if w, k := c.ContextEngine.KVCacheWarningThresh, c.ContextEngine.KVCacheCriticalThresh; w > 1 || k > 1 || w < 0 || k < 0 {
		return fmt.Errorf("kv_cache thresholds must be within [0, 1], got warning=%v critical=%v", w, k)
	}
	if j := c.Execution.JudgePersona; j != "" {
		if _, ok := c.Personas.Role(j); !ok {
			return fmt.Errorf("execution.judge_persona %q is not a defined persona role", j)
		}
	}
	if n := c.Execution.DivergenceCandidates; n != 0 && n < 1 {
		return fmt.Errorf("execution.divergence_candidates must be >= 1, got %d", n)
	}
	if v := c.Execution.MinJudgeConfidence; v != nil && (*v < 0 || *v > 1) {
		return fmt.Errorf("execution.min_judge_confidence must be within [0, 1], got %v", *v)
	}
	// F4 policy block (ADR-0044..0046): enum and range checks run before
	// defaults so a typo is rejected with an actionable error.
	switch p := c.Execution.Policy.ComputePolicy; p {
	case "", ComputePolicyDefault, ComputePolicyAdaptive:
	default:
		return fmt.Errorf("execution.policy.compute_policy must be %q or %q, got %q",
			ComputePolicyDefault, ComputePolicyAdaptive, p)
	}
	switch m := c.Execution.Policy.JudgeMode; m {
	case "", JudgeModeLLM, JudgeModeVerifier:
	default:
		return fmt.Errorf("execution.policy.judge_mode must be %q or %q, got %q",
			JudgeModeLLM, JudgeModeVerifier, m)
	}
	switch n := c.Execution.Policy.NoveltyReset; n {
	case "", NoveltyResetAuto, NoveltyResetReset, NoveltyResetKeep:
	default:
		return fmt.Errorf("execution.policy.novelty_reset must be %q, %q, or %q, got %q",
			NoveltyResetAuto, NoveltyResetReset, NoveltyResetKeep, n)
	}
	for name, v := range map[string]*float64{
		"execution.policy.verifier_min_fraction": c.Execution.Policy.VerifierMinFraction,
		"execution.policy.min_anchor_agreement":  c.Execution.Policy.MinAnchorAgreement,
		"execution.policy.jaccard_floor":         c.Execution.Policy.JaccardFloor,
		"execution.policy.quality_tie_margin":    c.Execution.Policy.QualityTieMargin,
	} {
		if v != nil && (*v < 0 || *v > 1) {
			return fmt.Errorf("%s must be within [0, 1], got %v", name, *v)
		}
	}
	if c.Execution.Policy.JudgeCount < 0 {
		return fmt.Errorf("execution.policy.judge_count must be >= 0, got %d", c.Execution.Policy.JudgeCount)
	}
	if v := c.Execution.Policy.MaxDiversityRerolls; v != nil && *v < 0 {
		return fmt.Errorf("execution.policy.max_diversity_rerolls must be >= 0, got %d", *v)
	}
	switch p := c.Network.DefaultPolicy; p {
	case "", "deny", "allow":
	default:
		return fmt.Errorf("network.default_policy must be \"deny\" or \"allow\", got %q", p)
	}
	// ADR-0011: the external API Host-header
	// allowlist must contain valid "host:port"
	// entries. Empty entries are rejected; an
	// empty list is allowed (disables the check,
	// documented escape hatch for tests).
	for _, entry := range c.Network.ExternalAPIHostAllowlist {
		if entry == "" {
			return fmt.Errorf("network.external_api_host_allowlist contains an empty entry")
		}
		if _, _, err := net.SplitHostPort(entry); err != nil {
			return fmt.Errorf("network.external_api_host_allowlist entry %q must be host:port: %w", entry, err)
		}
	}
	switch lvl := c.Logging.Level; lvl {
	case "", "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logging.level must be debug|info|warn|error, got %q", lvl)
	}
	known := map[string]bool{}
	for _, cat := range Categories {
		known[cat] = true
	}
	for _, cat := range c.Logging.Categories {
		if !known[cat] {
			return fmt.Errorf("logging.categories: unknown category %q (see ARCHITECTURE §28.1)", cat)
		}
	}
	// M2-T4: job_pod.default_tools must be a subset of the closed set.
	// toolenvelope.Parse is the single source of truth for the closed
	// set; we call it here so a typo in the config is rejected with
	// the same error a runtime would see.
	if _, err := toolenvelope.Parse(c.JobPod.DefaultTools); err != nil {
		return fmt.Errorf("job_pod.default_tools: %w", err)
	}
	// known-issues M2: bounded numeric fields must not accept negatives. A
	// typo like `max_concurrent_jobs: -3` (only 0 is replaced by a default)
	// previously loaded silently, violating the fail-loud contract.
	for _, n := range []struct {
		name string
		val  int
	}{
		{"agent.max_proactive_tasks_per_day", c.Agent.MaxProactiveTasksPerDay},
		{"limits.max_concurrent_jobs", c.Limits.MaxConcurrentJobs},
		{"limits.max_concurrent_llm_calls", c.Limits.MaxConcurrentLLMCalls},
		{"limits.max_concurrent_fetches", c.Limits.MaxConcurrentFetches},
		{"limits.max_tasks_per_hour", c.Limits.MaxTasksPerHour},
		{"limits.max_total_recoveries_per_job", c.Limits.MaxTotalRecoveriesPerJob},
		{"recovery.max_tool_call_retries", c.Recovery.MaxToolCallRetries},
		{"recovery.max_loop_interventions", c.Recovery.MaxLoopInterventions},
		{"recovery.max_context_compactions", c.Recovery.MaxContextCompactions},
		{"backup.max_local_backups", c.Backup.MaxLocalBackups},
		{"power.daydream_max_concurrent", c.Power.DaydreamMaxConcurrent},
		{"power.daydream_max_wall_time_minutes", c.Power.DaydreamMaxWallTimeMinutes},
		{"strategy_analysis.min_cohort_size", c.StrategyAnalysis.MinCohortSize},
		{"strategy_analysis.max_active_insights", c.StrategyAnalysis.MaxActiveInsights},
		{"strategy_analysis.insight_expiry_days", c.StrategyAnalysis.InsightExpiryDays},
		{"job_pod.pids_limit", c.JobPod.PidsLimit},
		{"job_pod.memory_mb", c.JobPod.MemoryMB},
		{"job_pod.tmpfs_mb", c.JobPod.TmpfsMB},
		{"network.rate_limit_per_minute", c.Network.RateLimitPerMinute},
	} {
		if n.val < 0 {
			return fmt.Errorf("%s must be >= 0, got %d", n.name, n.val)
		}
	}
	if c.JobPod.CPUs < 0 {
		return fmt.Errorf("job_pod.cpus must be >= 0, got %v", c.JobPod.CPUs)
	}
	if c.Network.MaxResponseBytes < 0 {
		return fmt.Errorf("network.max_response_bytes must be >= 0, got %d", c.Network.MaxResponseBytes)
	}
	for name, v := range map[string]float64{
		"strategy_analysis.min_accept_rate_delta":      c.StrategyAnalysis.MinAcceptRateDelta,
		"strategy_analysis.technique_min_accept_delta": c.StrategyAnalysis.TechniqueMinAcceptDelta,
	} {
		if v < 0 || v > 1 {
			return fmt.Errorf("%s must be within [0, 1], got %v", name, v)
		}
	}
	// M4-T7 (ADR-0019 §4): the search_web engine template is validated
	// at load so an operator typo fails at boot, not at first search.
	// The template must parse as text/template, must reference `.Query`
	// (a template without it would always fetch the same page), and
	// must render to an http(s) URL with a host when executed with an
	// escaped sample query. Unknown template fields fail the Execute
	// below — a typo like `{{.query}}` is rejected with the template
	// engine's own error, which names the offending field.
	if t := c.Network.SearchEngineURLTemplate; t != "" {
		tpl, err := template.New("search_engine_url_template").Parse(t)
		if err != nil {
			return fmt.Errorf("network.search_engine_url_template: %w", err)
		}
		if !strings.Contains(t, ".Query") {
			return fmt.Errorf("network.search_engine_url_template must reference {{.Query}}; got %q", t)
		}
		var buf bytes.Buffer
		if err := tpl.Execute(&buf, map[string]string{"Query": url.QueryEscape("athanor template validation")}); err != nil {
			return fmt.Errorf("network.search_engine_url_template: %w", err)
		}
		u, err := url.Parse(buf.String())
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("network.search_engine_url_template must render to an http(s) URL with a host; rendered %q", buf.String())
		}
	}
	return nil
}

// validateCross checks constraints that span fields after defaults.
func validateCross(c *Config) error {
	w, k := c.ContextEngine.KVCacheWarningThresh, c.ContextEngine.KVCacheCriticalThresh
	if w <= 0 || w > 1 || k <= 0 || k > 1 {
		return fmt.Errorf("kv_cache thresholds must be within (0, 1], got warning=%v critical=%v", w, k)
	}
	if w >= k {
		return fmt.Errorf("kv_cache_warning_threshold (%v) must be below kv_cache_critical_threshold (%v)", w, k)
	}
	if _, ok := c.Personas.Role(c.Execution.JudgePersona); !ok {
		return fmt.Errorf("execution.judge_persona %q is not a defined persona role", c.Execution.JudgePersona)
	}
	// M3-T4 commit 4.3: MaxHardTaskVariations must be ≥ 1
	// when set. The §13 dialectical loop uses this as an
	// upper bound on candidate count; a 0 or negative value
	// is a config bug that the engine would silently swallow
	// (its `if max > 0` guard in `phaseDivergeN` was the
	// M3-T1 fix for the bad default, but a load-time check
	// surfaces the typo with an actionable error). The
	// reflection-loops sibling gets the same check.
	if c.Execution.MaxHardTaskVariations < 1 {
		return fmt.Errorf("execution.max_hard_task_variations must be ≥ 1, got %d", c.Execution.MaxHardTaskVariations)
	}
	if v := c.Execution.MaxReflectionLoops; v != nil && *v < 0 {
		return fmt.Errorf("execution.max_reflection_loops must be >= 0, got %d", *v)
	}
	// M6-T1 (ADR-0032): the DAG bounds must be positive after defaults.
	// A zero here until now meant "no bound" in the pure validator, but in
	// configuration it is a typo the operator should hear about.
	if c.Execution.DAGMaxTasks < 1 {
		return fmt.Errorf("execution.dag_max_tasks must be ≥ 1, got %d", c.Execution.DAGMaxTasks)
	}
	if c.Execution.DAGMaxDepth < 1 {
		return fmt.Errorf("execution.dag_max_depth must be ≥ 1, got %d", c.Execution.DAGMaxDepth)
	}
	if c.Execution.DAGMaxTotalJobs < 1 {
		return fmt.Errorf("execution.dag_max_total_jobs must be ≥ 1, got %d", c.Execution.DAGMaxTotalJobs)
	}
	// M6-T3 (ADR-0034): retries are a non-negative count; an explicit 0 is
	// valid (retries disabled).
	if v := c.Execution.MaxTaskRetries; v != nil && *v < 0 {
		return fmt.Errorf("execution.max_task_retries must be ≥ 0, got %d", *v)
	}
	// M8-T11: the self-refine budget is a non-negative count; 0 disables it
	// even when self_refine is true.
	if v := c.Execution.MaxSelfRefineLoops; v != nil && *v < 0 {
		return fmt.Errorf("execution.max_self_refine_loops must be ≥ 0, got %d", *v)
	}
	// M6-T4 (ADR-0035): HITL windows must be positive after defaults.
	if c.HITL.DefaultTTL <= 0 {
		return fmt.Errorf("hitl.default_ttl must be positive, got %s", c.HITL.DefaultTTL)
	}
	if c.HITL.ExpiryInterval <= 0 {
		return fmt.Errorf("hitl.expiry_interval must be positive, got %s", c.HITL.ExpiryInterval)
	}
	// M4-T2/T3/T4 (ADR-0015): the airlock thresholds are
	// post-default values (validateCross runs after applyDefaults).
	// MaxIngressBytes must be positive; the ratios and
	// thresholds must be positive. The pipeline lists are
	// not validated here: the closed set lives in
	// internal/airlock/scanner, which errors at registry
	// construction on an unknown in-tree name. Keeping the
	// registry as the single source of truth means adding
	// a scanner name does not require editing this file.
	if c.Airlock.MaxIngressBytes <= 0 {
		return fmt.Errorf("airlock.max_ingress_bytes must be positive, got %d", c.Airlock.MaxIngressBytes)
	}
	if c.Airlock.MaxUncompressedRatio < 1 {
		return fmt.Errorf("airlock.max_uncompressed_ratio must be ≥ 1, got %d", c.Airlock.MaxUncompressedRatio)
	}
	if c.Airlock.MaxZipEntries < 1 {
		return fmt.Errorf("airlock.max_zip_entries must be ≥ 1, got %d", c.Airlock.MaxZipEntries)
	}
	if c.Airlock.PromptInjectionLongUserPromptThresholdBytes < 1 {
		return fmt.Errorf("airlock.prompt_injection_long_user_prompt_threshold_bytes must be ≥ 1, got %d", c.Airlock.PromptInjectionLongUserPromptThresholdBytes)
	}
	if err := validateUnenforced(c); err != nil {
		return err
	}
	return nil
}

// validateUnenforced rejects a non-default value for a field that is parsed
// and validated but not yet enforced by production code. The contract is
// "a validated field either works or fails loudly": silently accepting a
// tuning knob that changes nothing is worse than refusing it. Implement the
// field (and delete its row here) or keep the documented default.
func validateUnenforced(c *Config) error {
	for _, f := range []struct {
		name string
		got  int
		def  int
	}{
		{"limits.max_tasks_per_hour", c.Limits.MaxTasksPerHour, 20},
		{"limits.max_total_recoveries_per_job", c.Limits.MaxTotalRecoveriesPerJob, 8},
		{"recovery.max_tool_call_retries", c.Recovery.MaxToolCallRetries, 2},
		{"recovery.max_loop_interventions", c.Recovery.MaxLoopInterventions, 1},
		{"recovery.max_context_compactions", c.Recovery.MaxContextCompactions, 3},
		{"agent.max_proactive_tasks_per_day", c.Agent.MaxProactiveTasksPerDay, 10},
		{"power.daydream_max_concurrent", c.Power.DaydreamMaxConcurrent, 2},
		{"context_engine.research_floor", c.ContextEngine.ResearchFloor, 32768},
		{"strategy_analysis.insight_expiry_days", c.StrategyAnalysis.InsightExpiryDays, 90},
		{"airlock.prompt_injection_long_user_prompt_threshold_bytes", c.Airlock.PromptInjectionLongUserPromptThresholdBytes, 2048},
	} {
		if f.got != f.def {
			return fmt.Errorf("%s is parsed but not enforced yet; keep the default %d or remove the field (ARCHITECTURE §29)", f.name, f.def)
		}
	}
	if c.Execution.Policy.VerifierMinFraction != nil && *c.Execution.Policy.VerifierMinFraction != 0.5 {
		return fmt.Errorf("execution.policy.verifier_min_fraction is parsed but not enforced yet; keep the default 0.5")
	}
	if c.Execution.Policy.MinAnchorAgreement != nil && *c.Execution.Policy.MinAnchorAgreement != 0.6 {
		return fmt.Errorf("execution.policy.min_anchor_agreement is parsed but not enforced yet; keep the default 0.6")
	}
	if !Val(c.Security.ScanIngressFiles, true) || !Val(c.Security.ScanEgressFiles, true) ||
		!Val(c.Security.PromptInjectionScan, true) || !Val(c.Security.QuarantineSuspiciousFiles, true) {
		return fmt.Errorf("security.* scan toggles are not independently enforced yet; keep them true (scanning is always on)")
	}
	if !Val(c.Airlock.PromptInjectionScanLongUserPrompts, true) {
		return fmt.Errorf("airlock.prompt_injection_scan_long_user_prompts is parsed but not enforced yet; keep the default true")
	}
	if c.Network.BrowserModeRequiresApproval != nil && !*c.Network.BrowserModeRequiresApproval {
		return fmt.Errorf("network.browser_mode_requires_approval is dormant until Browser Mode lands; keep the default true")
	}
	if c.Backup.IncludeWorkspaceMetadata != nil && !*c.Backup.IncludeWorkspaceMetadata {
		return fmt.Errorf("backup.include_workspace_metadata is not implemented (only the database is snapshotted); keep the default true")
	}
	return nil
}

// Warnings returns actionable non-fatal configuration warnings. The daemon
// prints them at boot; unlike validateUnenforced they do not refuse startup.
func (c *Config) Warnings() []string {
	var out []string
	if c.Inference.DefaultBackend == "cloud" {
		out = append(out, "inference.default_backend=cloud is reserved and not implemented; the daemon uses Ollama")
	}
	if c.Inference.CloudEnabled {
		out = append(out, "inference.cloud_enabled=true has no effect in 0.0.0 (no cloud client); the daemon uses Ollama")
	}
	return out
}

// checkActiveHours validates an "HH:MM-HH:MM" range such as "00:00-24:00".
func checkActiveHours(s string) error {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return fmt.Errorf("%q must match HH:MM-HH:MM", s)
	}
	start, err := parseClock(parts[0], false)
	if err != nil {
		return fmt.Errorf("%q: %w", s, err)
	}
	end, err := parseClock(parts[1], true)
	if err != nil {
		return fmt.Errorf("%q: %w", s, err)
	}
	if end <= start {
		return fmt.Errorf("%q: end must be after start", s)
	}
	return nil
}

// parseClock parses "HH:MM". allow24 admits "24:00" (end-of-day sentinel).
func parseClock(s string, allow24 bool) (int, error) {
	bad := fmt.Errorf("%q is not a valid HH:MM time", s)
	fields := strings.Split(s, ":")
	if len(fields) != 2 {
		return 0, bad
	}
	hh, err1 := strconv.Atoi(fields[0])
	mm, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil {
		return 0, bad
	}
	maxHour := 23
	if allow24 {
		maxHour = 24
	}
	if hh < 0 || hh > maxHour || mm < 0 || mm > 59 {
		return 0, bad
	}
	if allow24 && hh == 24 && mm != 0 {
		return 0, bad
	}
	return hh*60 + mm, nil
}
