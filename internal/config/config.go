// Package config loads and validates the Athanor daemon configuration
// (ARCHITECTURE.md §29). Optional fields receive documented defaults;
// malformed, wrong-typed, or semantically invalid configurations are
// rejected with actionable errors.
package config

import (
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/jobpod"
	"github.com/tcs76321/athanor/internal/toolenvelope"
	"gopkg.in/yaml.v3"
)

// CurrentVersion is the only supported schema version.
const CurrentVersion = 2

// Duration is a time.Duration that unmarshals from YAML strings such as
// "5m", "120s", or "1h30m".
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler. Durations must be quoted
// strings ("5m"); bare numbers are rejected with an actionable message.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Tag != "!!str" {
		return fmt.Errorf("duration must be a quoted string like \"5m\", got %s", value.Tag)
	}
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a quoted string like \"5m\": %w", err)
	}
	dd, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if dd <= 0 {
		return fmt.Errorf("duration %q must be positive", s)
	}
	*d = Duration(dd)
	return nil
}

// String returns the canonical duration representation.
func (d Duration) String() string { return time.Duration(d).String() }

// D parses the value as a standard time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// MarshalYAML renders the canonical string form so `athanor init` output
// round-trips through Load.
func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

// Config is the complete daemon configuration (§29).
type Config struct {
	Version          int              `yaml:"version"`
	Agent            Agent            `yaml:"agent"`
	Power            Power            `yaml:"power"`
	Inference        Inference        `yaml:"inference"`
	Personas         Personas         `yaml:"personas"`
	ContextEngine    ContextEngine    `yaml:"context_engine"`
	Execution        Execution        `yaml:"execution"`
	StrategyAnalysis StrategyAnalysis `yaml:"strategy_analysis"`
	Limits           Limits           `yaml:"limits"`
	Recovery         Recovery         `yaml:"recovery"`
	Network          Network          `yaml:"network"`
	Security         Security         `yaml:"security"`
	Backup           Backup           `yaml:"backup"`
	Logging          Logging          `yaml:"logging"`
	JobPod           JobPod           `yaml:"job_pod"`
	Airlock          Airlock          `yaml:"airlock"`
	HITL             HITL             `yaml:"hitl"`

	// SourcePath is set by Load and records where the config came from.
	SourcePath string `yaml:"-"`
}

// Agent configures autonomous working hours and proactivity.
type Agent struct {
	ActiveHours             string   `yaml:"active_hours"`
	IdleThreshold           Duration `yaml:"idle_threshold"`
	MaxProactiveTasksPerDay int      `yaml:"max_proactive_tasks_per_day"`
}

// Power configures endurance/idle policy (§24).
type Power struct {
	RequireACForDeepWork         *bool    `yaml:"require_ac_for_deep_work"`
	BatteryPauseThresholdPercent int      `yaml:"battery_pause_threshold_percent"`
	IdleResumeAfter              Duration `yaml:"idle_resume_after"`
	AllowBatteryOverride         bool     `yaml:"allow_battery_override"`
	PauseOnSleep                 *bool    `yaml:"pause_on_sleep"`
	ResumeOnWake                 *bool    `yaml:"resume_on_wake"`
	DaydreamOnIdle               *bool    `yaml:"daydream_on_idle"`
	DaydreamMaxConcurrent        int      `yaml:"daydream_max_concurrent"`
	DaydreamMaxWallTimeMinutes   int      `yaml:"daydream_max_wall_time_minutes"`
}

// Inference selects backends and gates cloud usage.
type Inference struct {
	DefaultBackend        string `yaml:"default_backend"`
	OllamaURL             string `yaml:"ollama_url"`
	CloudEnabled          bool   `yaml:"cloud_enabled"`
	CloudRequiresApproval *bool  `yaml:"cloud_requires_approval"`
	// JSONFormat constrains the security persona's structured phases
	// (evaluating, comparing) to valid JSON at the wire layer
	// (ADR-0012). A pointer distinguishes "unset" (default true) from an
	// explicit false, which the M3-T7 quality probe uses for a format
	// ablation. Resolve via JSONFormatEnabled.
	JSONFormat *bool `yaml:"json_format"`
	// JSONSchema upgrades the judgment phases from `format: "json"` to a
	// JSON schema (M3-T7.5b, ADR-0042). Off by default: the M3-T7 smoke
	// found that schema-constrained decoding on ornith-1.5:9b can run away
	// on an unbounded string field (one candidate ran >7 min with no
	// output), whereas `"json"` returns promptly. The tolerant verdict
	// parser (M3-T7.5a) handles the type drift the schema was meant to
	// prevent, so the schema is an opt-in experiment, not the default.
	JSONSchema bool `yaml:"json_schema"`
	// MaxOutputTokens caps tokens generated per LLM call (Ollama's
	// num_predict) — the runaway guard that keeps one degenerate
	// generation from eating the per-call timeout (M3-T7.6b). 0 is
	// replaced by the 4096 default; negative is rejected.
	MaxOutputTokens int `yaml:"max_output_tokens"`
	// Think controls a thinking-capable model's reasoning phase (Ollama's
	// `think`). Defaults to false: a local-first deployment favors
	// bounded, cheap calls, and a thinking model's reasoning can otherwise
	// consume the output-token budget and leave the visible content empty
	// (M3-T7 smoke). Set true to allow reasoning.
	Think *bool `yaml:"think"`
	// JudgmentSeed controls sampler-seed pinning on Temperature-0
	// judgment calls (M3-T7.1). JudgmentSeedOff (default) leaves the
	// seed unset so Ollama draws a random one per request;
	// JudgmentSeedDerived pins a deterministic seed derived from the
	// job, phase, and candidate bytes and records it in the llm_call
	// audit row. Divergence is never seeded.
	JudgmentSeed string `yaml:"judgment_seed"`
}

// Judgment-seed policy values for Inference.JudgmentSeed.
const (
	// JudgmentSeedOff leaves the Ollama sampler seed unset (random per
	// request). This is the production-faithful default.
	JudgmentSeedOff = "off"
	// JudgmentSeedDerived pins a deterministic seed on Temperature-0
	// calls, derived from stable inputs and recorded for audit.
	JudgmentSeedDerived = "derived"
)

// JSONFormatEnabled reports whether structured judgment phases request
// Ollama JSON mode, applying the documented default (true) when the
// operator left json_format unset. Explicit false disables it.
func (i Inference) JSONFormatEnabled() bool {
	return Val(i.JSONFormat, true)
}

// PersonaConfig assigns a model to one functional role (§12).
// Temperature is a pointer so an explicitly configured value survives
// defaults resolution (0.0 is meaningful for the security persona);
// Temp() resolves it after Load/Parse.
//
// Family is the model's lineage — who made it and which generation
// (e.g. "qwen", "gemma4", "llama3.1", "granite4.2"). F4-T3 requires the
// deciding judge to come from a different family than the generator so
// their errors are decorrelated. It is operator-declared: two tags from
// the same lineage (gemma4:4b judging gemma4:12b) share a family and add
// no independent signal, so a match is a mismatch we must not paper over
// with a name guess. Empty falls back to the model name with its tag
// stripped (defaults.go).
type PersonaConfig struct {
	Model         string   `yaml:"model"`
	Family        string   `yaml:"family"`
	ContextTarget int      `yaml:"context_target"`
	Temperature   *float64 `yaml:"temperature"`
}

// Temp resolves the persona temperature. Valid only on a Config returned
// from Load or Parse (defaults guarantee non-nil).
func (p PersonaConfig) Temp() float64 {
	if p.Temperature == nil {
		return 0
	}
	return *p.Temperature
}

// Personas maps the five fixed functional roles to model assignments.
type Personas struct {
	Wide        PersonaConfig `yaml:"wide"`
	Tall        PersonaConfig `yaml:"tall"`
	Main        PersonaConfig `yaml:"main"`
	Security    PersonaConfig `yaml:"security"`
	Alternative PersonaConfig `yaml:"alternative"`
}

// Role returns the persona assignment for a named role.
func (p *Personas) Role(name string) (PersonaConfig, bool) {
	switch name {
	case "wide":
		return p.Wide, true
	case "tall":
		return p.Tall, true
	case "main":
		return p.Main, true
	case "security":
		return p.Security, true
	case "alternative":
		return p.Alternative, true
	default:
		return PersonaConfig{}, false
	}
}

// ContextEngine configures MCE floors and thresholds (§10, §12.6).
type ContextEngine struct {
	CodingFloor            int     `yaml:"coding_floor"`
	ResearchFloor          int     `yaml:"research_floor"`
	DocumentFloor          int     `yaml:"document_floor"`
	SimpleFloor            int     `yaml:"simple_floor"`
	CompactionTemperature  float64 `yaml:"compaction_temperature"`
	EnableLosslessSwapping *bool   `yaml:"enable_lossless_swapping"`
	KVCacheWarningThresh   float64 `yaml:"kv_cache_warning_threshold"`
	KVCacheCriticalThresh  float64 `yaml:"kv_cache_critical_threshold"`
	// M5-T2 division bounds (§10.1). A source larger than
	// DivisionMaxSourceBytes is skipped with a `context` audit row rather
	// than divided; a single chunk larger than DivisionMaxChunkBytes is
	// byte-split so one oversized declaration cannot bloat a row.
	// DivisionFallbackLines is the fixed block size of the universal
	// fallback splitter.
	DivisionMaxSourceBytes int64 `yaml:"division_max_source_bytes"`
	DivisionMaxChunkBytes  int   `yaml:"division_max_chunk_bytes"`
	DivisionFallbackLines  int   `yaml:"division_fallback_lines"`
	// M5-T7 memory retrieval (§10.2, §25; ADR-0026 §7).
	// MemoryEmbeddingModel names the Ollama embedding model used for the
	// vector half of query_memory. Empty (the default) makes the vector
	// half inert and retrieval full-text only. MemorySearchTopK caps the
	// hits a single query returns; it must be positive.
	MemoryEmbeddingModel string `yaml:"memory_embedding_model"`
	MemorySearchTopK     int    `yaml:"memory_search_top_k"`
	// M5-T8 repository indexing (ADR-0028 §8). IndexBatchFiles bounds the
	// files a single pass processes; IndexBatchChunks bounds the summary +
	// embedding model calls per pass; IndexEmbedBytes is the content prefix
	// in an embedding digest (explicit 0 = path + summary only); and
	// IndexIgnoreDirs are extra directory names skipped in addition to the
	// built-in VCS/dependency/build set.
	IndexBatchFiles  int      `yaml:"index_batch_files"`
	IndexBatchChunks int      `yaml:"index_batch_chunks"`
	IndexEmbedBytes  *int     `yaml:"index_embed_bytes"`
	IndexIgnoreDirs  []string `yaml:"index_ignore_dirs"`
}

// IndexEmbedBytesValue resolves index_embed_bytes, applying the documented
// 2048 default only when the key was absent. An explicit 0 is honored and
// means "path + summary only" (ADR-0028 §4).
func (c *ContextEngine) IndexEmbedBytesValue() int {
	if c.IndexEmbedBytes == nil {
		return 2048
	}
	return *c.IndexEmbedBytes
}

// LosslessSwapping reports whether §10.1 division and swapping are enabled,
// applying the documented default (true) when the operator left the field
// unset. Explicit false disables ingestion and the M5-T3 swap.
func (c *ContextEngine) LosslessSwapping() bool {
	return Val(c.EnableLosslessSwapping, true)
}

// Execution configures the dialectical loop (§13, §19).
type Execution struct {
	DivergenceCandidates  int `yaml:"divergence_candidates"`
	MaxHardTaskVariations int `yaml:"max_hard_task_variations"`
	// MaxReflectionLoops bounds the reflect→re-diverge loop. The pointer
	// distinguishes "unset" (default 2) from an explicit 0 (reflection
	// disabled) — the M3-T7 probe uses 0 for a pure single-shot baseline,
	// and F4-T2 makes it a policy decision. Resolve via
	// MaxReflectionLoopsValue.
	MaxReflectionLoops *int   `yaml:"max_reflection_loops"`
	JudgePersona       string `yaml:"judge_persona"`
	// The three flags below are M3-deferred: declared and
	// defaulted to true so the shipped example config validates
	// and parses, but the engine does not yet consult them.
	// Operators who set any of these to false today will see
	// no behavior change. They become effective in M6/M7. See
	// ROADMAP §7 and the M3 close-out entry that documents
	// the deferral. The pointer types are used so the
	// defaults package can distinguish "unset" (apply true)
	// from "explicitly false" (still no behavior change in
	// M3, but the field shape is ready for M6/M7 to read).
	RequireTestsForCode         *bool `yaml:"require_tests_for_code"`
	RequireDocumentationForCode *bool `yaml:"require_documentation_for_code"`
	CompareBeforeAccept         *bool `yaml:"compare_before_accept"`
	// MinJudgeConfidence is the §19.3 deterministic guard
	// threshold. The pointer type lets the operator explicitly
	// disable the guard by setting the value to 0 (the
	// documented disabled sentinel): `*float64` distinguishes
	// "unset" (use the default 0.7) from "explicitly zero"
	// (disable). The same pattern is used for the `*bool` fields
	// above (where "unset" and "explicitly false" must be
	// distinguishable). Consumers that need a plain float64
	// resolve via `Execution.MinJudge()` below, which applies
	// the 0.7 default when the field is nil.
	MinJudgeConfidence   *float64            `yaml:"min_judge_confidence"`
	PhaseWallTimeBudgets map[string]Duration `yaml:"phase_wall_time_budgets"`
	// M6-T1 DAG decomposition bounds (ADR-0032). The pure validator in
	// internal/dag rejects a graph that exceeds any of these. Zero is
	// replaced by a default in defaults.go; a non-positive value after
	// defaults is a config error.
	DAGMaxTasks     int `yaml:"dag_max_tasks"`
	DAGMaxDepth     int `yaml:"dag_max_depth"`
	DAGMaxTotalJobs int `yaml:"dag_max_total_jobs"`
	// DAGDecomposition enables the M6-T2 decompose-then-schedule path on
	// goal submission (ADR-0033 §5). Default false preserves the M1
	// single-task walking skeleton; set true for autonomous DAG execution.
	DAGDecomposition bool `yaml:"dag_decomposition"`
	// MaxTaskRetries bounds how many times a failed task is retried before
	// the §7.2 policy blocks it and attempts re-decomposition or HITL
	// escalation (M6-T3, ADR-0034). The pointer distinguishes "unset"
	// (default 2) from an explicit 0 (retries disabled); a task's own
	// budget.max_jobs, when set, is a tighter bound.
	MaxTaskRetries *int `yaml:"max_task_retries"`
	// Policy is the F4 compute/selection river (ADR-0044/0045/0046). It
	// parameterizes the policy seam, verification-first selection, judge
	// quorum/calibration, diversity enforcement, and cost-aware
	// acceptance. Every field has a documented default; the defaults
	// reproduce pre-F4 behavior except where noted (corrections are
	// recommendations only).
	Policy PolicyConfig `yaml:"policy"`
}

// PolicyConfig parameterizes F4's adaptive judgment and verification
// (ADR-0044..0046). It is a river: it allocates compute and selects
// models, and never touches security constraints, containment, the
// pinned judge temperature, or HITL rules.
type PolicyConfig struct {
	// ComputePolicy selects the pure policy implementation: "default"
	// reproduces today's fixed N/reflection; "adaptive" lowers compute on
	// easy or familiar tasks (F4-T2). It can only lower spend, never
	// exceed execution.divergence_candidates / max_reflection_loops.
	ComputePolicy string `yaml:"compute_policy"`
	// JudgeMode selects how accept/reject is decided: "llm" is today's
	// security-persona judge; "verifier" runs deterministic per-archetype
	// verifiers first and consults the LLM judge only on ties or when no
	// verifier exists (F4-T3).
	JudgeMode string `yaml:"judge_mode"`
	// JudgeCount is the number of independent judge calls for a
	// high-stakes accept; >1 means quorum (F4-T4). 1 is a single call.
	JudgeCount int `yaml:"judge_count"`
	// VerifierMinFraction is the fraction of code accepts that must be
	// decided by deterministic verifiers when JudgeMode=verifier
	// (F4-T3 acceptance).
	VerifierMinFraction *float64 `yaml:"verifier_min_fraction"`
	// MinAnchorAgreement is the ranking agreement a judge must reach on
	// the eval/anchor set before it may decide (F4-T4).
	MinAnchorAgreement *float64 `yaml:"min_anchor_agreement"`
	// JaccardFloor is the minimum mean pairwise Jaccard distance a
	// divergence set must reach before it is accepted; a below-floor set
	// is re-rolled at most MaxDiversityRerolls times (F4-T5).
	JaccardFloor *float64 `yaml:"jaccard_floor"`
	// MaxDiversityRerolls bounds the below-floor re-roll loop (F4-T5).
	// The pointer distinguishes "unset" (default 1) from an explicit 0
	// (never re-roll).
	MaxDiversityRerolls *int `yaml:"max_diversity_rerolls"`
	// CostAware lets a quality tie break toward the lower-cost artifact
	// (F4-T6). When false, comparison ignores cost.
	CostAware *bool `yaml:"cost_aware"`
	// QualityTieMargin is the score delta within which two artifacts are
	// considered tied for cost-aware acceptance (F4-T6).
	QualityTieMargin *float64 `yaml:"quality_tie_margin"`
	// RequireCrossFamily refuses the verifier/judge path when the judge
	// and generator personas share a family (F4-T3). When false, the
	// mismatch is audited but not blocked.
	RequireCrossFamily *bool `yaml:"require_cross_family"`
	// HeterogeneousDiversity cycles divergence candidates across the
	// generator and `alternative` personas so a non-trivial task draws
	// from more than one source (F4-T5). Default true.
	HeterogeneousDiversity *bool `yaml:"heterogeneous_diversity"`
}

// Policy-mode enum values.
const (
	ComputePolicyDefault  = "default"
	ComputePolicyAdaptive = "adaptive"

	JudgeModeLLM      = "llm"
	JudgeModeVerifier = "verifier"
)

// ComputePolicySelection resolves execution.policy.compute_policy with the
// documented "default" fallback.
func (e *Execution) ComputePolicySelection() string {
	if e.Policy.ComputePolicy == "" {
		return ComputePolicyDefault
	}
	return e.Policy.ComputePolicy
}

// JudgeModeSelection resolves execution.policy.judge_mode with the
// documented "llm" fallback (backward compatible).
func (e *Execution) JudgeModeSelection() string {
	if e.Policy.JudgeMode == "" {
		return JudgeModeLLM
	}
	return e.Policy.JudgeMode
}

// JudgeCountValue resolves execution.policy.judge_count with a 1 fallback.
func (e *Execution) JudgeCountValue() int {
	if e.Policy.JudgeCount < 1 {
		return 1
	}
	return e.Policy.JudgeCount
}

// VerifierMinFractionValue resolves verifier_min_fraction (default 0.5).
func (e *Execution) VerifierMinFractionValue() float64 {
	if e.Policy.VerifierMinFraction == nil {
		return 0.5
	}
	return *e.Policy.VerifierMinFraction
}

// MinAnchorAgreementValue resolves min_anchor_agreement (default 0.6).
func (e *Execution) MinAnchorAgreementValue() float64 {
	if e.Policy.MinAnchorAgreement == nil {
		return 0.6
	}
	return *e.Policy.MinAnchorAgreement
}

// JaccardFloorValue resolves jaccard_floor (default 0.30 — the M3-T7-a
// low-divergence trigger).
func (e *Execution) JaccardFloorValue() float64 {
	if e.Policy.JaccardFloor == nil {
		return 0.30
	}
	return *e.Policy.JaccardFloor
}

// MaxDiversityRerollsValue resolves max_diversity_rerolls (default 0 — the
// re-roll is opt-in; the Jaccard metric is always audited).
func (e *Execution) MaxDiversityRerollsValue() int {
	if e.Policy.MaxDiversityRerolls == nil {
		return 0
	}
	return *e.Policy.MaxDiversityRerolls
}

// CostAwareEnabled resolves cost_aware (default true).
func (e *Execution) CostAwareEnabled() bool {
	return Val(e.Policy.CostAware, true)
}

// QualityTieMarginValue resolves quality_tie_margin (default 0.02).
func (e *Execution) QualityTieMarginValue() float64 {
	if e.Policy.QualityTieMargin == nil {
		return 0.02
	}
	return *e.Policy.QualityTieMargin
}

// RequireCrossFamilyValue resolves require_cross_family (default true).
func (e *Execution) RequireCrossFamilyValue() bool {
	return Val(e.Policy.RequireCrossFamily, true)
}

// HeterogeneousDiversityEnabled resolves heterogeneous_diversity (default
// true): divergence candidates cycle across the generator and `alternative`
// personas (F4-T5).
func (e *Execution) HeterogeneousDiversityEnabled() bool {
	return Val(e.Policy.HeterogeneousDiversity, true)
}

// MaxTaskRetriesValue resolves execution.max_task_retries, applying the
// documented 2 default when the operator left the field unset. An explicit 0
// is returned as 0 (retries disabled). This is the only call site consumers
// should use; reading the raw pointer is reserved for the config layer.
func (e *Execution) MaxTaskRetriesValue() int {
	if e.MaxTaskRetries == nil {
		return 2
	}
	return *e.MaxTaskRetries
}

// MaxReflectionLoopsValue resolves execution.max_reflection_loops,
// applying the documented 2 default when unset. An explicit 0 is honored
// (reflection disabled).
func (e *Execution) MaxReflectionLoopsValue() int {
	if e.MaxReflectionLoops == nil {
		return 2
	}
	return *e.MaxReflectionLoops
}

// MinJudge returns the §19.3 guard threshold, applying the
// 0.7 default when the operator left the field unset.
// Explicit 0 is returned as 0 (the disabled sentinel —
// DecideWinner treats `threshold <= 0` as "every record
// meets the bar"). This is the only call site consumers
// should use; reading the raw `*float64` field is reserved
// for the config layer (defaults, validation, serialization).
func (e *Execution) MinJudge() float64 {
	if e.MinJudgeConfidence == nil {
		return 0.7
	}
	return *e.MinJudgeConfidence
}

// PhaseBudget returns the wall-time budget for a phase, falling back to the
// "default" budget when the phase has no specific entry.
func (e *Execution) PhaseBudget(phase string) (time.Duration, bool) {
	if d, ok := e.PhaseWallTimeBudgets[phase]; ok {
		return d.D(), true
	}
	d, ok := e.PhaseWallTimeBudgets["default"]
	return d.D(), ok
}

// StrategyAnalysis configures win/loss mining (§13.3–13.4).
type StrategyAnalysis struct {
	Enabled                *bool   `yaml:"enabled"`
	MinCohortSize          int     `yaml:"min_cohort_size"`
	MinAcceptRateDelta     float64 `yaml:"min_accept_rate_delta"`
	AutoPromote            bool    `yaml:"auto_promote"`
	MaxActiveInsights      int     `yaml:"max_active_insights"`
	InsightExpiryDays      int     `yaml:"insight_expiry_days"`
	StrategyNotesInPrompts *bool   `yaml:"strategy_notes_in_prompts"`
}

// Limits bounds concurrency and cost.
type Limits struct {
	MaxConcurrentJobs        int `yaml:"max_concurrent_jobs"`
	MaxConcurrentLLMCalls    int `yaml:"max_concurrent_llm_calls"`
	MaxConcurrentFetches     int `yaml:"max_concurrent_fetches"`
	MaxTasksPerHour          int `yaml:"max_tasks_per_hour"`
	MaxTotalRecoveriesPerJob int `yaml:"max_total_recoveries_per_job"`
}

// Recovery bounds retry behavior.
type Recovery struct {
	MaxToolCallRetries    int `yaml:"max_tool_call_retries"`
	MaxLoopInterventions  int `yaml:"max_loop_interventions"`
	MaxContextCompactions int `yaml:"max_context_compactions"`
}

// Network configures the Internet Gated Reader (§21.5)
// and the external API Host-header allowlist (ADR-0011).
type Network struct {
	DefaultPolicy               string   `yaml:"default_policy"`
	AllowList                   []string `yaml:"allow_list"`
	RateLimitPerMinute          int      `yaml:"rate_limit_per_minute"`
	MaxResponseBytes            int64    `yaml:"max_response_bytes"`
	ReaderModeDefault           *bool    `yaml:"reader_mode_default"`
	BrowserModeRequiresApproval *bool    `yaml:"browser_mode_requires_approval"`
	// ExternalAPIHostAllowlist is the set of
	// "host:port" pairs the external API accepts
	// (ADR-0011 §D1). Requests whose Host header is
	// not in this set are rejected with 421
	// Misdirected Request. An empty list disables
	// the check (a documented escape hatch for
	// tests; never the default in production).
	ExternalAPIHostAllowlist []string `yaml:"external_api_host_allowlist"`
	// SearchEngineURLTemplate is the M4-T7 `search_web`
	// backend (ADR-0019 §4): a `text/template` URL with
	// a single `{{.Query}}` action. Empty (the default)
	// leaves the `search_web` tool inert — calls return
	// the typed ErrSearchNotConfigured. The template is
	// a convenience for constructing the query URL; the
	// built URL is NOT exempt from the gateway allowlist.
	// Validated at load: must parse, must render to an
	// http(s) URL with a host, and must reference
	// `.Query` (a template without it would always fetch
	// the same page).
	SearchEngineURLTemplate string `yaml:"search_engine_url_template"`
}

// ReaderMode returns the §21.5 reader-mode flag, applying the `true`
// default when the operator left the field unset. An explicit `false`
// disables Reader Mode extraction (ADR-0018 §5); the gateway then
// refuses extraction with ErrReaderDisabled and callers fall back to
// the raw fetched bytes. The resolver mirrors the `Execution.MinJudge()`
// pointer-field pattern: consumers resolve through the method; reading
// the raw `*bool` is reserved for the config layer.
func (n *Network) ReaderMode() bool {
	if n.ReaderModeDefault == nil {
		return true
	}
	return *n.ReaderModeDefault
}

// Security toggles airlock scanning (§21.3–21.4).
type Security struct {
	ScanIngressFiles          *bool `yaml:"scan_ingress_files"`
	ScanEgressFiles           *bool `yaml:"scan_egress_files"`
	PromptInjectionScan       *bool `yaml:"prompt_injection_scan"`
	QuarantineSuspiciousFiles *bool `yaml:"quarantine_suspicious_files"`
}

// Backup configures automatic backups (§23.4).
type Backup struct {
	Auto                     *bool  `yaml:"auto"`
	Schedule                 string `yaml:"schedule"`
	MaxLocalBackups          int    `yaml:"max_local_backups"`
	IncludeWorkspaceMetadata *bool  `yaml:"include_workspace_metadata"`
}

// Logging configures level and enabled event-log categories (§28).
type Logging struct {
	Level      string   `yaml:"level"`
	Categories []string `yaml:"categories"`
}

// JobPod configures per-job tool allowlists and Job Pod image
// resolution (ARCHITECTURE §25, ROADMAP M2-T4).
//
// DefaultTools is the per-job tool envelope applied when a task does
// not declare its own override. An empty list is a valid default and
// means "no tools" — the engine still runs the LLM-only phases.
//
// Image is the resolved image reference used for ephemeral Job Pods.
// Production sets it explicitly. When empty the daemon still boots
// (LLM-only jobs are useful) but logs a warning at boot and refuses to
// dispatch any Job Pod tool call with a 503
// (internalapi.ErrExecNotConfigured; ADR-0024 §6). Empty in unit tests
// that use a fake engine.ToolRunner.
//
// ResourceLimits override the §21.2 defaults. A zero value in any
// field means "use the jobpod default" (see jobpod.Limits).
type JobPod struct {
	DefaultTools []string `yaml:"default_tools"`
	Image        string   `yaml:"image"`
	PidsLimit    int      `yaml:"pids_limit"`
	MemoryMB     int      `yaml:"memory_mb"`
	CPUs         float64  `yaml:"cpus"`
}

// JobPodEnvelope returns the per-job default tool envelope. The
// result is the closed-set list from c.JobPod.DefaultTools. A
// non-nil error indicates a closed-set violation and should fail
// the daemon at boot.
func (c *Config) JobPodEnvelope() (toolenvelope.Envelope, error) {
	return toolenvelope.Parse(c.JobPod.DefaultTools)
}

// JobPodResourceLimits converts the config's JobPod block into a
// jobpod.Limits. Zero values are passed through; jobpod.withDefaults
// fills them at pod-creation time. The translation lives here so
// the YAML schema does not have to mention the jobpod package.
func (c *Config) JobPodResourceLimits() jobpod.Limits {
	return jobpod.Limits{
		PidsLimit: c.JobPod.PidsLimit,
		MemoryMB:  c.JobPod.MemoryMB,
		CPUs:      c.JobPod.CPUs,
	}
}

// Airlock configures the §21.3 file-airlock pipeline (ROADMAP M4-T2/T3/T4;
// ADR-0015). The block is the per-pipeline scanner selection plus the
// numeric thresholds the in-tree scanners (size, zipbomb, prompt-injection
// heuristic) consult. The "scanner absent" failure mode is uniform across
// all three pipeline lists: a named scanner the registry cannot instantiate
// (e.g. "clamav" without a clamdscan binary on PATH) degrades to
// VerdictUncertain at scan time, so the pipeline fails closed.
//
// Scanners is a per-pipeline list because the three choke points are
// asymmetric on purpose (see ADR-0015 §"Trust boundaries, not all text"):
//
//   - Ingress: full set (heuristic + size + zipbomb + clamav + yara)
//   - Egress: never prompt-injection-scanned (LLM-generated data)
//   - UserPrompt: only the heuristic, only for prompts over the threshold
//
// MaxIngressBytes, MaxUncompressedRatio, MaxZipEntries are the §21.3
// numeric limits applied by the in-tree `size` and `zipbomb` scanners.
// PromptInjectionLongUserPromptThresholdBytes and
// PromptInjectionScanLongUserPrompts gate the goal-submit heuristic
// (default-on, 2 KiB threshold; see ADR-0015 §"Defense in depth at every
// crossing").
//
// YaraRuleSet is the path (relative to stateDir or absolute) the in-tree
// YARA adapter loads rules from. Empty string disables the rule set;
// the adapter then reports `Available() == false` and degrades.
type Airlock struct {
	Enabled                                     *bool           `yaml:"enabled"`
	Scanners                                    AirlockScanners `yaml:"scanners"`
	MaxIngressBytes                             int64           `yaml:"max_ingress_bytes"`
	MaxUncompressedRatio                        int             `yaml:"max_uncompressed_ratio"`
	MaxZipEntries                               int             `yaml:"max_zip_entries"`
	PromptInjectionLongUserPromptThresholdBytes int             `yaml:"prompt_injection_long_user_prompt_threshold_bytes"`
	PromptInjectionScanLongUserPrompts          *bool           `yaml:"prompt_injection_scan_long_user_prompts"`
	YaraRuleSet                                 string          `yaml:"yara_rule_set"`
}

// AirlockScanners is the per-pipeline scanner list. Each entry is a
// registered scanner name (the closed set is documented in
// internal/airlock/scanner; M4-T3 expands the package). An empty list
// is a valid configuration: a pipeline with no scanners accepts
// everything, which is the safe default for tests that exercise the
// pipeline wiring without the scanner implementations.
type AirlockScanners struct {
	Ingress    []string `yaml:"ingress"`
	Egress     []string `yaml:"egress"`
	UserPrompt []string `yaml:"user_prompt"`
}

// HITL configures the §20 human-in-the-loop queue (M6-T4; ADR-0035).
// DefaultTTL bounds how long a pending request waits before it expires and
// denies by default; ExpiryInterval is how often the daemon sweeps overdue
// requests.
type HITL struct {
	DefaultTTL     Duration `yaml:"default_ttl"`
	ExpiryInterval Duration `yaml:"expiry_interval"`
}
