package main

import (
	"fmt"
	"strings"
)

// probePolicy carries the F4 follow-up run knobs into the generated config:
// the judge mode, an optional cross-family `security` model, an optional
// second `alternative` model (multi-model generation), and the F5 code
// acceptance gates.
type probePolicy struct {
	JudgeMode  string // "" | "llm" | "verifier"
	JudgeModel string // personas.security.model override
	AltModel   string // personas.alternative.model override
	OllamaURL  string // inference.ollama_url override ("" = localhost:11434)
	// CodeGates enables execution.require_tests_for_code and
	// require_documentation_for_code (F5). The probe defaults them OFF so the
	// headline dialectical-vs-single-shot comparison isolates the loop: the
	// F5 gates post-date the M3-T7 baseline, and the documentation gate is a
	// heuristic that would reject valid code goals (e.g. cache-class,
	// always-reverse) whose criteria do not mention docstrings. Run the
	// gates as a separate arm (-gates) to measure their effect.
	CodeGates bool
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// probeConfigYAML renders the daemon config for one (model, arm, seed)
// run. It is generated rather than checked in so the arm configs cannot
// drift from the matrix. seedPolicy is "off" or "derived"
// (inference.judgment_seed); divergence is never seeded. addr must be in
// the external API Host-header allowlist (ADR-0011), or the daemon
// rejects every request on a non-default port.
//
// Context targets: every generation/evaluation role meets §12.6's
// coding_floor (32768) so code goals do not pause on a floor violation;
// `tall` keeps its constrained 16384 because §12.6 exempts it during
// planning. job_pod.default_tools grants the code-archetype tools the
// engine's evaluation sub-steps require — without it, code jobs
// soft-fail without ever running tests.
func probeConfigYAML(m probeModel, a arm, seedPolicy, addr string, noReflect bool, p probePolicy) string {
	reflectLine := ""
	if noReflect {
		reflectLine = "  max_reflection_loops: 0\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `version: 2

inference:
  default_backend: ollama
  ollama_url: %q
  json_format: true
  judgment_seed: %q
  max_output_tokens: 4096
  think: false
  keep_alive: "5m"

network:
  external_api_host_allowlist:
    - %q

personas:
  wide:
    model: %q
    context_target: %d
    temperature: 0.7
  tall:
    model: %q
    context_target: 16384
    temperature: 0.2
  main:
    model: %q
    context_target: %d
    temperature: 0.4
  security:
    model: %q
    context_target: %d
    temperature: 0.0
  alternative:
    model: %q
    context_target: %d
    temperature: 0.8

execution:
  divergence_candidates: %d
  require_tests_for_code: %t
  require_documentation_for_code: %t
`,
		orDefault(p.OllamaURL, "http://localhost:11434"), seedPolicy, addr,
		m.Model, m.ContextTarget,
		m.Model,
		m.Model, m.ContextTarget,
		orDefault(p.JudgeModel, m.Model), m.ContextTarget,
		orDefault(p.AltModel, m.Model), m.ContextTarget,
		a.Candidates,
		p.CodeGates, p.CodeGates,
	)
	if p.JudgeMode != "" {
		fmt.Fprintf(&b, "  policy:\n    judge_mode: %q\n", p.JudgeMode)
	}
	b.WriteString(reflectLine)
	// `comparing` must accommodate a model swap under single residency
	// (ARCHITECTURE §12.5), so it is larger than the M3-T7 120s.
	b.WriteString(`  phase_wall_time_budgets:
    planning: "900s"
    diverging: "900s"
    evaluating: "900s"
    synthesizing: "900s"
    comparing: "600s"
    default: "900s"

limits:
  max_concurrent_jobs: 1

job_pod:
  image: "python:3.12-alpine"
  default_tools:
    - execute_code
    - run_tests
    - lint
`)
	return b.String()
}
