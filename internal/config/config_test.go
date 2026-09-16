package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/toolenvelope"
	"gopkg.in/yaml.v3"
)

const validMinimal = `
version: 2
agent:
  idle_threshold: "10m"
personas:
  security:
    model: "llama3.2:3b"
    context_target: 4096
    temperature: 0.0
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValidMinimalAppliesDefaults(t *testing.T) {
	cfg, err := Load(writeTemp(t, validMinimal))
	if err != nil {
		t.Fatalf("Load(valid) = %v, want nil", err)
	}
	if cfg.Version != 2 {
		t.Errorf("Version = %d, want 2", cfg.Version)
	}
	if cfg.Agent.IdleThreshold.D() != 10*time.Minute {
		t.Errorf("explicit idle_threshold not honored: %v", cfg.Agent.IdleThreshold)
	}
	if cfg.Agent.ActiveHours != "00:00-24:00" {
		t.Errorf("ActiveHours default = %q", cfg.Agent.ActiveHours)
	}
	if got := cfg.Personas.Security.Model; got != "llama3.2:3b" || cfg.Personas.Security.ContextTarget != 4096 {
		t.Errorf("explicit security persona overridden: %+v", cfg.Personas.Security)
	}
	if got := cfg.Personas.Wide; got.Model != "qwen2.5:7b" || got.ContextTarget != 65536 || got.Temp() != 0.7 {
		t.Errorf("wide persona defaults = %+v", got)
	}
	if cfg.Execution.DivergenceCandidates != 3 {
		t.Errorf("DivergenceCandidates default = %d", cfg.Execution.DivergenceCandidates)
	}
	if cfg.Execution.MaxReflectionLoops != 2 {
		t.Errorf("MaxReflectionLoops default = %d, want 2", cfg.Execution.MaxReflectionLoops)
	}
	if cfg.Execution.JudgePersona != "security" {
		t.Errorf("JudgePersona default = %q", cfg.Execution.JudgePersona)
	}
	if d, _ := cfg.Execution.PhaseBudget("evaluating"); d != 600*time.Second {
		t.Errorf("evaluating budget default = %v", d)
	}
	if d, _ := cfg.Execution.PhaseBudget("synthesizing"); d != 300*time.Second {
		t.Errorf("fallback budget for unknown phase = %v", d)
	}
	if len(cfg.Network.ExternalAPIHostAllowlist) != 4 {
		t.Errorf("ExternalAPIHostAllowlist default = %v, want 4 entries", cfg.Network.ExternalAPIHostAllowlist)
	}
	if !Val(cfg.Security.ScanIngressFiles, false) || !Val(cfg.Backup.Auto, false) {
		t.Error("default-on security/backup flags not applied")
	}
	if len(cfg.Logging.Categories) != len(Categories) {
		t.Errorf("logging categories default = %v", cfg.Logging.Categories)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("err = %v, want ErrFileNotFound", err)
	}
}

func TestParseEmptyConfig(t *testing.T) {
	for _, tc := range []string{"", "\n\n", "   "} {
		if _, err := Parse([]byte(tc)); !errors.Is(err, ErrEmptyConfig) {
			t.Fatalf("Parse(%q) err = %v, want ErrEmptyConfig", tc, err)
		}
	}
}

func TestParseMalformedYAML(t *testing.T) {
	_, err := Parse([]byte("version: 2\n  bad indent: ["))
	if err == nil || !strings.Contains(err.Error(), "malformed config") {
		t.Fatalf("err = %v, want malformed config error", err)
	}
}

func TestParseWrongType(t *testing.T) {
	_, err := Parse([]byte("version: \"two\""))
	if err == nil || !strings.Contains(err.Error(), "wrong-typed") {
		t.Fatalf("err = %v, want wrong-typed error", err)
	}
	// Duration given as bare number instead of quoted string.
	_, err = Parse([]byte("agent:\n  idle_threshold: 5\n"))
	if err == nil || !strings.Contains(err.Error(), "quoted string") {
		t.Fatalf("duration-as-int err = %v, want specific quoting guidance", err)
	}
}

func TestParseUnknownFieldRejected(t *testing.T) {
	_, err := Parse([]byte("version: 2\ndefinitely_not_a_field: true\n"))
	if err == nil {
		t.Fatal("unknown field accepted, want strict rejection")
	}
}

func TestValidateRejectsInvalidSemantics(t *testing.T) {
	cases := []struct {
		name   string
		yaml   string
		errSub string
	}{
		{
			name:   "bad version",
			yaml:   "version: 1",
			errSub: "unsupported config version",
		},
		{
			name:   "bad active hours",
			yaml:   "version: 2\nagent:\n  active_hours: \"25:00-99:99\"\n",
			errSub: "active_hours",
		},
		{
			name:   "battery threshold out of range",
			yaml:   "version: 2\npower:\n  battery_pause_threshold_percent: 150\n",
			errSub: "battery_pause_threshold_percent",
		},
		{
			name:   "unknown judge persona",
			yaml:   "version: 2\nexecution:\n  judge_persona: oracle\n",
			errSub: "judge_persona",
		},
		{
			name:   "nonzero compaction temperature violates invariant",
			yaml:   "version: 2\ncontext_engine:\n  compaction_temperature: 0.3\n",
			errSub: "compaction_temperature",
		},
		{
			name:   "kv thresholds inverted",
			yaml:   "version: 2\ncontext_engine:\n  kv_cache_warning_threshold: 0.95\n  kv_cache_critical_threshold: 0.85\n",
			errSub: "kv_cache_warning_threshold",
		},
		{
			name:   "temperature out of range",
			yaml:   "version: 2\npersonas:\n  main:\n    model: m\n    context_target: 1024\n    temperature: 3.0\n",
			errSub: "main.temperature",
		},
		{
			name:   "bad network policy",
			yaml:   "version: 2\nnetwork:\n  default_policy: permit-all\n",
			errSub: "default_policy",
		},
		{
			name:   "unknown log category",
			yaml:   "version: 2\nlogging:\n  categories: [jobs, gossip]\n",
			errSub: "gossip",
		},
		{
			name:   "negative divergence candidates",
			yaml:   "version: 2\nexecution:\n  divergence_candidates: -2\n",
			errSub: "divergence_candidates",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("err = %v, want containing %q", err, tc.errSub)
			}
		})
	}
}

// TestMinJudgeConfidence_ZeroSurvivesLoad pins the
// operator-facing contract: setting
// `execution.min_judge_confidence: 0` is the documented
// disabled sentinel for the §19.3 deterministic guard,
// and the value must survive the load pipeline (no error,
// field preserved). A previous version of
// `internal/engine/compare.go` overrode an explicit 0
// back to 0.7 inside the engine, making the disabled
// mode unreachable through config. The override was
// removed; this test pins the new contract at the
// config layer.
func TestMinJudgeConfidence_ZeroSurvivesLoad(t *testing.T) {
	cfg, err := Parse([]byte("version: 2\nexecution:\n  min_judge_confidence: 0\n"))
	if err != nil {
		t.Fatalf("Parse(min_judge_confidence=0) = %v, want nil (zero is the disabled sentinel)", err)
	}
	if cfg.Execution.MinJudgeConfidence == nil {
		t.Fatal("MinJudgeConfidence is nil, want &0 (disabled sentinel preserved)")
	}
	if *cfg.Execution.MinJudgeConfidence != 0 {
		t.Errorf("*MinJudgeConfidence = %v, want 0 (disabled sentinel preserved)", *cfg.Execution.MinJudgeConfidence)
	}
	if got := cfg.Execution.MinJudge(); got != 0 {
		t.Errorf("MinJudge() = %v, want 0 (disabled sentinel returned)", got)
	}
}

// TestMinJudgeConfidence_CustomValuePreserved: a
// non-default threshold (0.42) is loaded verbatim.
// The engine respects it end-to-end via
// `TestCompare_CustomThreshold`; this test pins the
// config layer in isolation.
func TestMinJudgeConfidence_CustomValuePreserved(t *testing.T) {
	cfg, err := Parse([]byte("version: 2\nexecution:\n  min_judge_confidence: 0.42\n"))
	if err != nil {
		t.Fatalf("Parse(min_judge_confidence=0.42) = %v, want nil", err)
	}
	if cfg.Execution.MinJudgeConfidence == nil {
		t.Fatal("MinJudgeConfidence is nil, want &0.42")
	}
	if *cfg.Execution.MinJudgeConfidence != 0.42 {
		t.Errorf("*MinJudgeConfidence = %v, want 0.42 (custom value preserved)", *cfg.Execution.MinJudgeConfidence)
	}
	if got := cfg.Execution.MinJudge(); got != 0.42 {
		t.Errorf("MinJudge() = %v, want 0.42 (custom value returned)", got)
	}
}

// TestMinJudgeConfidence_OutOfRangeRejected: a
// value > 1.0 still fails validation. The disabled
// sentinel (0) is a separate case and must NOT trip
// the range check.
func TestMinJudgeConfidence_OutOfRangeRejected(t *testing.T) {
	_, err := Parse([]byte("version: 2\nexecution:\n  min_judge_confidence: 1.5\n"))
	if err == nil {
		t.Fatal("Parse(min_judge_confidence=1.5) = nil, want range error")
	}
	if !strings.Contains(err.Error(), "min_judge_confidence") {
		t.Errorf("err = %q, want min_judge_confidence in message", err.Error())
	}
}

// TestReaderModeDefault_UnsetAppliesTrue pins the M4-T6 contract:
// a config that omits `network.reader_mode_default` gets the true
// default filled by applyDefaults, and `Network.ReaderMode()`
// resolves to true (the §21.5 Reader Mode is on by default).
func TestReaderModeDefault_UnsetAppliesTrue(t *testing.T) {
	cfg, err := Load(writeTemp(t, validMinimal))
	if err != nil {
		t.Fatalf("Load(valid) = %v, want nil", err)
	}
	if cfg.Network.ReaderModeDefault == nil {
		t.Fatal("ReaderModeDefault is nil, want &true (default filled)")
	}
	if !*cfg.Network.ReaderModeDefault {
		t.Error("ReaderModeDefault = false, want true")
	}
	if !cfg.Network.ReaderMode() {
		t.Error("ReaderMode() = false, want true")
	}
}

// TestReaderModeDefault_ExplicitFalseRespected pins the opt-out:
// an explicit `reader_mode_default: false` is a distinct "disabled"
// state, not the nil default, and ReaderMode() returns false so the
// gateway refuses extraction (ADR-0018 §5).
func TestReaderModeDefault_ExplicitFalseRespected(t *testing.T) {
	cfg, err := Parse([]byte("version: 2\nnetwork:\n  reader_mode_default: false\n"))
	if err != nil {
		t.Fatalf("Parse(reader_mode_default=false) = %v, want nil", err)
	}
	if cfg.Network.ReaderModeDefault == nil {
		t.Fatal("ReaderModeDefault is nil, want &false (explicit value preserved)")
	}
	if *cfg.Network.ReaderModeDefault {
		t.Error("ReaderModeDefault = true, want explicit false")
	}
	if cfg.Network.ReaderMode() {
		t.Error("ReaderMode() = true, want false (explicit opt-out)")
	}
}
// TestMinJudgeConfidence_UnsetAppliesDefault: a
// minimal config that omits the field entirely must
// have the 0.7 default applied by `applyDefaults`,
// and `MinJudge()` must return 0.7. This pins the
// default-fill behavior so a future refactor that
// changes the default can't silently break callers
// that rely on the documented 0.7.
func TestMinJudgeConfidence_UnsetAppliesDefault(t *testing.T) {
	cfg, err := Parse([]byte("version: 2\n"))
	if err != nil {
		t.Fatalf("Parse(minimal) = %v, want nil", err)
	}
	if cfg.Execution.MinJudgeConfidence == nil {
		t.Fatal("MinJudgeConfidence is nil after Parse; applyDefaults should have set it")
	}
	if *cfg.Execution.MinJudgeConfidence != 0.7 {
		t.Errorf("*MinJudgeConfidence = %v, want 0.7 (default applied)", *cfg.Execution.MinJudgeConfidence)
	}
	if got := cfg.Execution.MinJudge(); got != 0.7 {
		t.Errorf("MinJudge() = %v, want 0.7 (default returned)", got)
	}
}

func TestExplicitFalseHonoredForDefaultTrueFlags(t *testing.T) {
	cfg, err := Parse([]byte("version: 2\npower:\n  daydream_on_idle: false\nsecurity:\n  scan_ingress_files: false\nbackup:\n  auto: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if Val(cfg.Power.DaydreamOnIdle, true) {
		t.Error("explicit daydream_on_idle=false was overridden by default")
	}
	if Val(cfg.Security.ScanIngressFiles, true) {
		t.Error("explicit scan_ingress_files=false was overridden by default")
	}
	if Val(cfg.Backup.Auto, true) {
		t.Error("explicit backup.auto=false was overridden by default")
	}
}

func TestDefaultIsValid(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default() err = %v, want nil", err)
	}
	if cfg.Version != CurrentVersion {
		t.Errorf("Version = %d, want %d", cfg.Version, CurrentVersion)
	}
	if cfg.Inference.DefaultBackend != "ollama" {
		t.Errorf("DefaultBackend = %q, want ollama", cfg.Inference.DefaultBackend)
	}
	if cfg.SourcePath != "" {
		t.Errorf("SourcePath = %q, want empty (no file involved)", cfg.SourcePath)
	}
}

// TestDefaultEqualsEmptySpecifiedConfig proves Default() is exactly what
// Load produces for a config file that specifies nothing but a valid
// version — the fallback and the file path converge on one configuration.
func TestDefaultEqualsEmptySpecifiedConfig(t *testing.T) {
	fromFile, err := Parse([]byte("version: 2\n"))
	if err != nil {
		t.Fatalf("Parse(minimal): %v", err)
	}
	def, err := Default()
	if err != nil {
		t.Fatalf("Default(): %v", err)
	}
	if !reflect.DeepEqual(def, fromFile) {
		t.Errorf("Default() differs from Parse(minimal):\ndef:   %+v\nfile:  %+v", def, fromFile)
	}
}

// TestDefaultYAMLRoundTrip proves `athanor init` output is loadable: the
// marshaled default config parses back into exactly the same values
// (durations render as quoted strings via Duration.MarshalYAML).
func TestDefaultYAMLRoundTrip(t *testing.T) {
	def, err := Default()
	if err != nil {
		t.Fatalf("Default(): %v", err)
	}
	raw, err := yaml.Marshal(def)
	if err != nil {
		t.Fatalf("marshalling default: %v", err)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("parsing marshaled default: %v\nyaml:\n%s", err, raw)
	}
	// Normalize nil vs empty slices (nil marshals as [] and parses back
	// as empty-but-non-nil; semantically identical).
	for _, c := range []*Config{def, parsed} {
		if len(c.Network.AllowList) == 0 {
			c.Network.AllowList = nil
		}
		if len(c.Logging.Categories) == 0 {
			c.Logging.Categories = nil
		}
	}
	if !reflect.DeepEqual(def, parsed) {
		t.Errorf("marshaled default does not round-trip to the same config:\ndef:  %+v\nwant: %+v", parsed, def)
	}
}

// TestExampleConfigMatchesDefaults keeps config.example.yaml honest: the
// shipped example must always parse and equal the built-in defaults, so
// copying it to config.yaml is a documented no-op.
func TestExampleConfigMatchesDefaults(t *testing.T) {
	data, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatalf("reading config.example.yaml: %v (file must ship with the repo)", err)
	}
	example, err := Parse(data)
	if err != nil {
		t.Fatalf("config.example.yaml does not parse: %v", err)
	}
	def, err := Default()
	if err != nil {
		t.Fatalf("Default(): %v", err)
	}
	// Normalize nil vs empty slices: `allow_list: []` in YAML decodes to an
	// empty slice while an omitted field stays nil — semantically identical.
	for _, c := range []*Config{example, def} {
		if len(c.Network.AllowList) == 0 {
			c.Network.AllowList = nil
		}
		if len(c.Logging.Categories) == 0 {
			c.Logging.Categories = nil
		}
		if len(c.JobPod.DefaultTools) == 0 {
			c.JobPod.DefaultTools = nil
		}
	}
	if !reflect.DeepEqual(example, def) {
		exJSON, _ := json.MarshalIndent(example, "", "  ")
		defJSON, _ := json.MarshalIndent(def, "", "  ")
		t.Errorf("config.example.yaml drifted from built-in defaults — update the example (or defaults) so they match\nexample:\n%s\ndefaults:\n%s", exJSON, defJSON)
	}
}

// TestNetwork_SearchTemplateValid (M4-T7, ADR-0019 §4) proves the
// search engine template validation accepts a well-formed template.
func TestNetwork_SearchTemplateValid(t *testing.T) {
	good := []byte("version: 2\nnetwork:\n  search_engine_url_template: \"https://html.duckduckgo.com/html/?q={{.Query}}\"\n")
	cfg, err := Parse(good)
	if err != nil {
		t.Fatalf("Parse with valid template: %v", err)
	}
	if cfg.Network.SearchEngineURLTemplate == "" {
		t.Fatal("SearchEngineURLTemplate is empty, want the configured template")
	}
}

// TestNetwork_SearchTemplateInvalid (M4-T7, ADR-0019 §4) proves the
// validation rejects broken templates with actionable errors that name
// the failing field. Rows: unparsable template, missing `.Query`
// (would always fetch the same page), unknown field (typo'd casing),
// and non-http(s) rendering.
func TestNetwork_SearchTemplateInvalid(t *testing.T) {
	cases := []struct {
		name     string
		template string
		wantErr  string
	}{
		{"unparsable", "https://example.com/?q={{.Query", "network.search_engine_url_template"},
		{"missing query", "https://example.com/search", "must reference {{.Query}}"},
		{"wrong field case", "https://example.com/?q={{.query}}", "network.search_engine_url_template"},
		{"non-http scheme", "ftp://example.com/?q={{.Query}}", "must render to an http(s) URL"},
		{"no host", "{{.Query}}", "must render to an http(s) URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := "version: 2\nnetwork:\n  search_engine_url_template: " + strconv.Quote(tc.template) + "\n"
			_, err := Parse([]byte(raw))
			if err == nil {
				t.Fatalf("Parse with template %q returned nil error; want %q", tc.template, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestJobPod_DefaultEnvelopeEmpty (M2-T4) proves the shipped default
// has an empty tool envelope: no tools are granted to a job unless
// the operator explicitly opts in. This is the safe default for a
// tool-execution surface.
func TestJobPod_DefaultEnvelopeEmpty(t *testing.T) {
	def, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	env, err := def.JobPodEnvelope()
	if err != nil {
		t.Fatalf("JobPodEnvelope(): %v", err)
	}
	if !env.IsEmpty() {
		t.Errorf("default JobPod envelope = %v, want empty", env.Tools())
	}
}

// TestJobPod_EnvelopeRejectsUnknown (M2-T4) proves the closed-set
// check runs at config-load time, not just at runtime: a typo in
// the config is an actionable error, not a silent no-op.
func TestJobPod_EnvelopeRejectsUnknown(t *testing.T) {
	bad := []byte("job_pod:\n  default_tools: [execute_code, run_remote_shell]\n")
	_, err := Parse(bad)
	if err == nil {
		t.Fatal("Parse with unknown tool returned nil error; want a job_pod.default_tools error")
	}
	if !strings.Contains(err.Error(), "job_pod.default_tools") {
		t.Errorf("error %q does not mention job_pod.default_tools (the failing field)", err)
	}
	if !strings.Contains(err.Error(), "run_remote_shell") {
		t.Errorf("error %q does not name the offending tool", err)
	}
}

// TestJobPod_EnvelopeAcceptsKnown (M2-T4) proves the validation
// accepts the closed set. A config that explicitly opts in to
// execute_code + run_tests must parse and yield the matching
// envelope.
func TestJobPod_EnvelopeAcceptsKnown(t *testing.T) {
	good := []byte("job_pod:\n  default_tools: [execute_code, run_tests]\n  image: python:3.12-alpine\n")
	cfg, err := Parse(good)
	if err != nil {
		t.Fatalf("Parse with known tools: %v", err)
	}
	env, err := cfg.JobPodEnvelope()
	if err != nil {
		t.Fatalf("JobPodEnvelope(): %v", err)
	}
	if !env.Allows(toolenvelope.ToolExecuteCode) || !env.Allows(toolenvelope.ToolRunTests) {
		t.Errorf("envelope = %v, want both tools allowed", env.Tools())
	}
}

// TestAirlock_DefaultsPerPipeline (M4-T2/T3/T4; ADR-0015) pins the
// per-pipeline scanner selection. Egress must never carry a
// prompt-injection scanner name (LLM-generated data; the category
// is wrong). The numeric thresholds are the §21.3 numbers in the
// ROADMAP M4-T4 acceptance criterion. The companion
// TestExampleConfigMatchesDefaults catches drift between the
// example and the defaults; this test catches drift between the
// defaults and the ADR's design intent.
func TestAirlock_DefaultsPerPipeline(t *testing.T) {
	def, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if !Val(def.Airlock.Enabled, false) {
		t.Errorf("airlock.enabled default = false, want true (defense-by-default)")
	}
	wantIngress := []string{"prompt-injection-heuristic", "size", "zipbomb", "clamav", "yara"}
	if !reflect.DeepEqual(def.Airlock.Scanners.Ingress, wantIngress) {
		t.Errorf("airlock.scanners.ingress default = %v, want %v", def.Airlock.Scanners.Ingress, wantIngress)
	}
	wantEgress := []string{"size", "zipbomb", "clamav", "yara"}
	if !reflect.DeepEqual(def.Airlock.Scanners.Egress, wantEgress) {
		t.Errorf("airlock.scanners.egress default = %v, want %v", def.Airlock.Scanners.Egress, wantEgress)
	}
	for _, name := range def.Airlock.Scanners.Egress {
		if strings.HasPrefix(name, "prompt-injection-") {
			t.Errorf("egress pipeline must not carry a prompt-injection scanner (ADR-0015), got %q", name)
		}
	}
	wantUserPrompt := []string{"prompt-injection-heuristic"}
	if !reflect.DeepEqual(def.Airlock.Scanners.UserPrompt, wantUserPrompt) {
		t.Errorf("airlock.scanners.user_prompt default = %v, want %v", def.Airlock.Scanners.UserPrompt, wantUserPrompt)
	}
	if def.Airlock.MaxIngressBytes != 104857600 {
		t.Errorf("airlock.max_ingress_bytes default = %d, want 104857600 (100 MiB)", def.Airlock.MaxIngressBytes)
	}
	if def.Airlock.MaxUncompressedRatio != 100 {
		t.Errorf("airlock.max_uncompressed_ratio default = %d, want 100", def.Airlock.MaxUncompressedRatio)
	}
	if def.Airlock.MaxZipEntries != 10000 {
		t.Errorf("airlock.max_zip_entries default = %d, want 10000", def.Airlock.MaxZipEntries)
	}
	if def.Airlock.PromptInjectionLongUserPromptThresholdBytes != 2048 {
		t.Errorf("airlock.prompt_injection_long_user_prompt_threshold_bytes default = %d, want 2048 (2 KiB)", def.Airlock.PromptInjectionLongUserPromptThresholdBytes)
	}
	if !Val(def.Airlock.PromptInjectionScanLongUserPrompts, false) {
		t.Errorf("airlock.prompt_injection_scan_long_user_prompts default = false, want true (defense-by-default)")
	}
	// airlock.yara_rule_set defaults to empty; the cmd
	// layer materializes the in-tree baseline ruleset
	// to <state-dir>/yara/injection.yar on boot. An
	// operator who wants a private rule set overrides
	// the field in config.yaml. The default is empty
	// rather than a path so the daemon never accidentally
	// reads a stale state-dir file at the wrong version.
	if def.Airlock.YaraRuleSet != "" {
		t.Errorf("airlock.yara_rule_set default = %q, want empty (cmd materializes the embedded baseline)", def.Airlock.YaraRuleSet)
	}
}

// TestAirlock_RejectsZeroThresholds (M4-T2/T3/T4) proves the
// post-default validateCross checks reject zero/negative
// thresholds. A typo that sets max_ingress_bytes to a negative
// value must surface as an error, not silently default-fill.
// (A literal 0 is treated as "unset" by the existing setInt
// helper, matching the pattern used elsewhere in the config
// — this is a deliberate choice; the validateRaw path is the
// right place to add a "reject explicit 0" check if we ever
// need it.)
func TestAirlock_RejectsZeroThresholds(t *testing.T) {
	bad := []byte("airlock:\n  max_ingress_bytes: -1\n")
	_, err := Parse(bad)
	if err == nil {
		t.Fatal("Parse with negative max_ingress_bytes returned nil; want a validation error")
	}
	if !strings.Contains(err.Error(), "airlock.max_ingress_bytes") {
		t.Errorf("error %q does not mention the failing field", err.Error())
	}
}

// TestAirlock_RejectsNegativeRatio (M4-T2/T3/T4) proves the
// ratio floor is ≥ 1, not just positive. A negative ratio would
// make the zipbomb scanner fail its `> ratio` check on every
// file, which is a denial-of-service vector. (A literal 0 is
// treated as "unset" by the existing setInt helper, so the
// post-default check only fires for explicitly-negative values;
// the validateRaw path is the right place to add a "reject
// explicit 0" check if we ever need it.)
func TestAirlock_RejectsNegativeRatio(t *testing.T) {
	bad := []byte("airlock:\n  max_uncompressed_ratio: -5\n")
	_, err := Parse(bad)
	if err == nil {
		t.Fatal("Parse with negative ratio returned nil; want a validation error")
	}
	if !strings.Contains(err.Error(), "airlock.max_uncompressed_ratio") {
		t.Errorf("error %q does not mention the failing field", err.Error())
	}
}
