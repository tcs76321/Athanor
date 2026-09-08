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
}

// PersonaConfig assigns a model to one functional role (§12).
// Temperature is a pointer so an explicitly configured value survives
// defaults resolution (0.0 is meaningful for the security persona);
// Temp() resolves it after Load/Parse.
type PersonaConfig struct {
	Model         string   `yaml:"model"`
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
}

// Execution configures the dialectical loop (§13, §19).
type Execution struct {
	DivergenceCandidates        int                 `yaml:"divergence_candidates"`
	MaxHardTaskVariations       int                 `yaml:"max_hard_task_variations"`
	MaxReflectionLoops          int                 `yaml:"max_reflection_loops"`
	JudgePersona                string              `yaml:"judge_persona"`
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
	RequireTestsForCode         *bool               `yaml:"require_tests_for_code"`
	RequireDocumentationForCode *bool               `yaml:"require_documentation_for_code"`
	CompareBeforeAccept         *bool               `yaml:"compare_before_accept"`
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
// Required in production: cmd/athanor/serve.go fails fast if Image is
// empty. Empty in unit tests that use a fake engine.ToolRunner.
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
	Enabled                                *bool    `yaml:"enabled"`
	Scanners                               AirlockScanners `yaml:"scanners"`
	MaxIngressBytes                        int64    `yaml:"max_ingress_bytes"`
	MaxUncompressedRatio                   int      `yaml:"max_uncompressed_ratio"`
	MaxZipEntries                          int      `yaml:"max_zip_entries"`
	PromptInjectionLongUserPromptThresholdBytes int  `yaml:"prompt_injection_long_user_prompt_threshold_bytes"`
	PromptInjectionScanLongUserPrompts     *bool    `yaml:"prompt_injection_scan_long_user_prompts"`
	YaraRuleSet                            string   `yaml:"yara_rule_set"`
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
