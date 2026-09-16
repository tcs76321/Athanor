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
	if c.Execution.MaxReflectionLoops < 1 {
		return fmt.Errorf("execution.max_reflection_loops must be ≥ 1, got %d", c.Execution.MaxReflectionLoops)
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
	return nil
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
