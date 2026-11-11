// Benchmark run protocol (eval/bench/protocol.yaml). This is the machine-
// readable half of the freeze: the task set is pinned by CORPUS.digest, and
// the models, digests, personas, context targets, inference parameters, and
// run shape are pinned here. Changing any of them is a new benchmark version.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type protocolRole struct {
	Model         string  `yaml:"model" json:"model"`
	Family        string  `yaml:"family" json:"family"`
	ContextTarget int     `yaml:"context_target" json:"context_target"`
	Temperature   float64 `yaml:"temperature" json:"temperature"`
}

type protocolInference struct {
	Backend         string `yaml:"backend" json:"backend"`
	EngineVersion   string `yaml:"engine_version" json:"engine_version"` // v3/v4 name
	OllamaVersion   string `yaml:"ollama_version" json:"ollama_version"` // v5 name
	OllamaURL       string `yaml:"ollama_url" json:"ollama_url"`
	JSONFormat      bool   `yaml:"json_format" json:"json_format"`
	MaxOutputTokens int    `yaml:"max_output_tokens" json:"max_output_tokens"`
	Think           bool   `yaml:"think" json:"think"`
	KeepAlive       string `yaml:"keep_alive" json:"keep_alive"`
	JudgmentSeed    string `yaml:"judgment_seed" json:"judgment_seed"`
}

type protocolExecution struct {
	DivergenceCandidates int  `yaml:"divergence_candidates" json:"divergence_candidates"`
	MaxReflectionLoops   int  `yaml:"max_reflection_loops" json:"max_reflection_loops"`
	EngineCodeGates      bool `yaml:"engine_code_gates" json:"engine_code_gates"`
	DAGDecomposition     bool `yaml:"dag_decomposition" json:"dag_decomposition"`
}

type protocolContext struct {
	SeedActiveChunk      bool   `yaml:"seed_active_chunk" json:"seed_active_chunk"`
	SeedActiveMaxBytes   int    `yaml:"seed_active_max_bytes" json:"seed_active_max_bytes"`
	RepositoryIndexLimit int    `yaml:"repository_index_limit" json:"repository_index_limit"`
	MemoryEmbeddingModel string `yaml:"memory_embedding_model" json:"memory_embedding_model"`
	MemorySearchTopK     int    `yaml:"memory_search_top_k" json:"memory_search_top_k"`
}

type protocolPersonaSet struct {
	Name      string                  `yaml:"name" json:"name"`
	Roles     map[string]protocolRole `yaml:"roles" json:"roles"`
	Inference protocolInference       `yaml:"inference" json:"inference"`
	Execution protocolExecution       `yaml:"execution" json:"execution"`
	Context   protocolContext         `yaml:"context" json:"context"`
	JobPod    struct {
		Image  string `yaml:"image" json:"image"`
		Digest string `yaml:"digest" json:"digest"`
	} `yaml:"job_pod" json:"job_pod"`
	Runtime struct {
		CorePodImage       string `yaml:"core_pod_image" json:"core_pod_image"`
		CorePodImageDigest string `yaml:"core_pod_image_digest" json:"core_pod_image_digest"`
		BenchImage         string `yaml:"bench_image" json:"bench_image"`
		BenchImageDigest   string `yaml:"bench_image_digest" json:"bench_image_digest"`
		HostClass          string `yaml:"host_class" json:"host_class"`
	} `yaml:"runtime" json:"runtime"`
}

// protocolJudgeRoles splits the roster's in-loop judge from the blind pairwise
// quality panel. A model can be a sound in-loop security judge and still fail
// the position-bias/agreement floor of the pairwise instrument.
type protocolJudgeRoles struct {
	InLoop  string   `yaml:"in_loop" json:"in_loop"`
	Quality []string `yaml:"quality" json:"quality"`
}

// protocolArm pins the candidates/reflection an arm actually runs. The runner's
// namedArm is the source of truth; this table makes the protocol agree with it.
type protocolArm struct {
	Candidates int `yaml:"candidates" json:"candidates"`
	Reflection int `yaml:"reflection" json:"reflection"`
}

type protocolExternal struct {
	AiderPolyglot struct {
		Manifest string `yaml:"manifest" json:"manifest"`
		Adapter  string `yaml:"adapter" json:"adapter"`
		Role     string `yaml:"role" json:"role"`
	} `yaml:"aider_polyglot" json:"aider_polyglot"`
}

type protocolRunShape struct {
	Full struct {
		Tasks              string  `yaml:"tasks"`
		Passes             int     `yaml:"passes"`
		Arm                string  `yaml:"arm"`
		MachineBudgetHours float64 `yaml:"machine_budget_hours"`
		PerJobTimeout      string  `yaml:"per_job_timeout"`
		MemoryScripts      string  `yaml:"memory_scripts"`
	} `yaml:"full"`
	Anchor struct {
		Tasks              string  `yaml:"tasks"`
		Passes             int     `yaml:"passes"`
		Arm                string  `yaml:"arm"`
		PerModelTimeout    string  `yaml:"per_model_timeout"`
		MachineBudgetHours float64 `yaml:"machine_budget_hours"`
	} `yaml:"anchor"`
}

type protocolRosterEntry struct {
	Label  string `yaml:"label" json:"label"`
	Model  string `yaml:"model" json:"model"`
	Family string `yaml:"family" json:"family"`
	Digest string `yaml:"digest" json:"digest"`
}

// protocolSuite is one claim suite in v3: a task file, independent protocol
// invocations, the arms that run it, and the held-out count. It is not a
// runner instruction by itself; `tools/bench` still runs one arm at a time.
type protocolSuite struct {
	Tasks              string   `yaml:"tasks" json:"tasks"`
	Passes             int      `yaml:"passes" json:"passes"`
	Arms               []string `yaml:"arms" json:"arms"`
	HeldOut            int      `yaml:"held_out" json:"held_out"`
	MachineBudgetHours float64  `yaml:"machine_budget_hours" json:"machine_budget_hours"`
}

type protocolNonInferiority struct {
	Comparison      string   `yaml:"comparison" json:"comparison"`
	Suites          []string `yaml:"suites" json:"suites"`
	MarginPoints    float64  `yaml:"margin_points" json:"margin_points"`
	MaxTaskLossRuns int      `yaml:"max_task_loss_runs" json:"max_task_loss_runs"`
}

type protocolAgentic struct {
	MechanismCoverage float64 `yaml:"mechanism_coverage" json:"mechanism_coverage"`
}

type protocolQuality struct {
	Status                     string  `yaml:"status" json:"status"`
	Comparison                 string  `yaml:"comparison" json:"comparison"`
	Suite                      string  `yaml:"suite" json:"suite"`
	MinMeanDelta               float64 `yaml:"min_mean_delta" json:"min_mean_delta"`
	MinCILower                 float64 `yaml:"min_ci_lower" json:"min_ci_lower"`
	MinWinRate                 float64 `yaml:"min_win_rate" json:"min_win_rate"`
	MinJudgeAgreement          float64 `yaml:"min_judge_agreement" json:"min_judge_agreement"`
	MinUsablePairs             int     `yaml:"min_usable_pairs" json:"min_usable_pairs"`
	MinHumanDirectionAgreement float64 `yaml:"min_human_direction_agreement" json:"min_human_direction_agreement"`
}

type protocolEarlyStop struct {
	Suite                string  `yaml:"suite"`
	MinStopRate          float64 `yaml:"min_stop_rate"`
	AgenticMinEscalation float64 `yaml:"agentic_min_escalation"`
}

// protocolUtilityComponent is one pre-registered utility gate: a named task
// set and how many of them must pass. Component gates replace the meaningless
// "90% of five tasks" aggregate with attributable claims.
type protocolUtilityComponent struct {
	Tasks   []string `yaml:"tasks" json:"tasks"`
	MinPass int      `yaml:"min_pass" json:"min_pass"`
}

type protocolUtility struct {
	Comparison          string                   `yaml:"comparison" json:"comparison"`
	Suite               string                   `yaml:"suite" json:"suite"`
	MarginPoints        float64                  `yaml:"margin_points" json:"margin_points"`
	MinCyclePassRate    float64                  `yaml:"min_cycle_pass_rate" json:"min_cycle_pass_rate"`
	MinCitationPassRate float64                  `yaml:"min_citation_pass_rate" json:"min_citation_pass_rate"` // v4
	MechanismCoverage   float64                  `yaml:"mechanism_coverage" json:"mechanism_coverage"`         // v4
	RequireNewsSequence bool                     `yaml:"require_news_sequence" json:"require_news_sequence"`   // v4
	Briefs              protocolUtilityComponent `yaml:"briefs" json:"briefs"`
	NewsTuning          protocolUtilityComponent `yaml:"news_tuning" json:"news_tuning"`
	NewsHeldOut         protocolUtilityComponent `yaml:"news_heldout" json:"news_heldout"`
	Completion          struct {
		MinRate float64 `yaml:"min_rate" json:"min_rate"`
	} `yaml:"completion" json:"completion"`
}

type protocolCost struct {
	SaturatedTokenRatioMax float64 `yaml:"saturated_token_ratio_max" json:"saturated_token_ratio_max"`
	SaturatedWallRatioMax  float64 `yaml:"saturated_wall_ratio_max" json:"saturated_wall_ratio_max"`
}

type protocolReliability struct {
	// AgencyPassAtTwo is "report" when pass@2 is reported but not a release
	// claim (n=5 is directional), or "claim" when it gates. v5 uses report.
	AgencyPassAtTwo string `yaml:"agency_pass_at_2" json:"agency_pass_at_2"`
}

type protocolMechanismClaim struct {
	Min float64 `yaml:"min" json:"min"`
}

type protocolSafetyClaim struct {
	MaxFailures int    `yaml:"max_failures" json:"max_failures"`
	Scope       string `yaml:"scope" json:"scope"`
}

type protocolClaims struct {
	NonInferiority    protocolNonInferiority `yaml:"non_inferiority" json:"non_inferiority"`
	Agentic           protocolAgentic        `yaml:"agentic" json:"agentic"`
	Quality           protocolQuality        `yaml:"quality" json:"quality"`
	EarlyStop         protocolEarlyStop      `yaml:"early_stop" json:"early_stop"`
	Utility           protocolUtility        `yaml:"utility" json:"utility"`
	Safety            protocolSafetyClaim    `yaml:"safety" json:"safety"`
	MechanismCoverage protocolMechanismClaim `yaml:"mechanism_coverage" json:"mechanism_coverage"`
	Reliability       protocolReliability    `yaml:"reliability" json:"reliability"`
	Cost              protocolCost           `yaml:"cost" json:"cost"`
	External          struct {
		Aider string `yaml:"aider" json:"aider"`
	} `yaml:"external" json:"external"`
}

type benchProtocol struct {
	Version             int                      `yaml:"version" json:"version"`
	Benchmark           string                   `yaml:"benchmark" json:"benchmark"`
	Cut                 string                   `yaml:"cut" json:"cut"`
	Status              string                   `yaml:"status" json:"status"`
	TaskDigest          string                   `yaml:"task_digest" json:"task_digest"`
	CanonicalPersonaSet protocolPersonaSet       `yaml:"canonical_persona_set" json:"canonical_persona_set"`
	RunShape            protocolRunShape         `yaml:"run_shape" json:"run_shape"`
	Roster              []protocolRosterEntry    `yaml:"roster" json:"roster"`
	Generators          []string                 `yaml:"generators" json:"generators"`
	Judges              []string                 `yaml:"judges" json:"judges"`
	JudgeRoles          protocolJudgeRoles       `yaml:"judge_roles" json:"judge_roles"`
	Arms                map[string]protocolArm   `yaml:"arms" json:"arms"`
	Suites              map[string]protocolSuite `yaml:"suites" json:"suites"`
	Claims              protocolClaims           `yaml:"claims" json:"claims"`
	External            protocolExternal         `yaml:"external" json:"external"`
	Embeddings          struct {
		Model  string `yaml:"model" json:"model"`
		Digest string `yaml:"digest" json:"digest"`
	} `yaml:"embeddings" json:"embeddings"`
	Scoring map[string]string `yaml:"scoring" json:"scoring"`
}

var protocolRoleNames = []string{"wide", "tall", "main", "security", "alternative"}

// loadProtocol reads and sanity-checks the run protocol.
func loadProtocol(path string) (benchProtocol, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return benchProtocol{}, fmt.Errorf("reading protocol %s: %w", path, err)
	}
	var p benchProtocol
	if err := yaml.Unmarshal(data, &p); err != nil {
		return benchProtocol{}, fmt.Errorf("parsing protocol %s: %w", path, err)
	}
	if p.Version < 1 || p.Version > 5 || p.Benchmark == "" {
		return benchProtocol{}, fmt.Errorf("protocol %s: unsupported version/benchmark", path)
	}
	if len(p.CanonicalPersonaSet.Roles) != len(protocolRoleNames) {
		return benchProtocol{}, fmt.Errorf("protocol %s: want %d persona roles, got %d",
			path, len(protocolRoleNames), len(p.CanonicalPersonaSet.Roles))
	}
	for _, role := range protocolRoleNames {
		if _, ok := p.CanonicalPersonaSet.Roles[role]; !ok {
			return benchProtocol{}, fmt.Errorf("protocol %s: missing persona role %q", path, role)
		}
	}
	if p.RunShape.Full.Passes != 1 {
		return benchProtocol{}, fmt.Errorf("protocol %s: full.passes = %d, want 1 (one full slice)",
			path, p.RunShape.Full.Passes)
	}
	if p.RunShape.Full.MachineBudgetHours <= 0 || p.RunShape.Full.MachineBudgetHours > 12 {
		return benchProtocol{}, fmt.Errorf("protocol %s: full.machine_budget_hours = %v, want (0, 12]",
			path, p.RunShape.Full.MachineBudgetHours)
	}
	if len(p.Roster) == 0 {
		return benchProtocol{}, fmt.Errorf("protocol %s: roster is empty", path)
	}
	if p.Version == 3 {
		if err := validateV3(p); err != nil {
			return benchProtocol{}, fmt.Errorf("protocol %s: %w", path, err)
		}
	}
	if p.Version == 4 {
		if err := validateV4(p); err != nil {
			return benchProtocol{}, fmt.Errorf("protocol %s: %w", path, err)
		}
	}
	if p.Version == 5 {
		if err := validateV5(p); err != nil {
			return benchProtocol{}, fmt.Errorf("protocol %s: %w", path, err)
		}
	}
	return p, nil
}

// validateV5 enforces the local-only grounded-agency contract: two local
// generators, a pinned roster, the five claim suites, the arm table, and the
// pre-registered release-blocking claim vector. It rejects a declared claim
// that has no computation path.
func validateV5(p benchProtocol) error {
	if p.Benchmark != "v5" {
		return fmt.Errorf("benchmark = %q, want v5", p.Benchmark)
	}
	if p.TaskDigest != "eval/bench/v5/CORPUS.digest" {
		return fmt.Errorf("task_digest = %q, want eval/bench/v5/CORPUS.digest", p.TaskDigest)
	}
	if len(p.Generators) != 2 || len(p.Judges) != 2 {
		return fmt.Errorf("generators/judges = %d/%d, want 2/2", len(p.Generators), len(p.Judges))
	}
	byLabel := map[string]protocolRosterEntry{}
	families := map[string]bool{}
	for _, e := range p.Roster {
		if e.Label == "" || e.Model == "" || e.Family == "" || e.Digest == "" {
			return fmt.Errorf("roster entry %+v is incomplete", e)
		}
		if _, dup := byLabel[e.Label]; dup {
			return fmt.Errorf("duplicate roster label %q", e.Label)
		}
		byLabel[e.Label] = e
	}
	// The v5 claim matrix is local-only. A frontier model may be added only in
	// a later benchmark version (ADR-0136 appendix).
	for _, label := range append(append([]string{}, p.Generators...), p.Judges...) {
		e, ok := byLabel[label]
		if !ok {
			return fmt.Errorf("model label %q is not in the roster", label)
		}
		if !isLocalBenchLabel(e.Label) {
			return fmt.Errorf("roster label %q is not a v5 local model", e.Label)
		}
		families[e.Family] = true
	}
	if len(families) != 4 {
		return fmt.Errorf("model families = %d, want 4 distinct families", len(families))
	}
	// The in-loop judge and the pairwise quality panel are distinct roles.
	if p.JudgeRoles.InLoop == "" {
		return fmt.Errorf("judge_roles.in_loop is empty")
	}
	if _, ok := byLabel[p.JudgeRoles.InLoop]; !ok {
		return fmt.Errorf("judge_roles.in_loop %q is not in the roster", p.JudgeRoles.InLoop)
	}
	for _, label := range p.JudgeRoles.Quality {
		if _, ok := byLabel[label]; !ok {
			return fmt.Errorf("judge_roles.quality %q is not in the roster", label)
		}
	}
	if p.Claims.Quality.Status != "measured" && p.Claims.Quality.Status != "not_measured" {
		return fmt.Errorf("quality.status = %q, want measured|not_measured", p.Claims.Quality.Status)
	}
	if p.Claims.Quality.Status == "measured" && len(p.JudgeRoles.Quality) < 2 {
		return fmt.Errorf("quality.status=measured needs at least two quality judges")
	}
	// The arms the protocol claims must match the runner's named arms.
	for _, name := range []string{"single", "full", "cycle"} {
		if _, ok := p.Arms[name]; !ok {
			return fmt.Errorf("missing arm %q", name)
		}
	}
	if p.Arms["single"].Candidates != 1 || p.Arms["single"].Reflection != 0 {
		return fmt.Errorf("single arm = %+v, want 1 candidate / 0 reflection", p.Arms["single"])
	}
	if p.Arms["full"].Candidates < 2 {
		return fmt.Errorf("full arm = %+v, want >= 2 candidates", p.Arms["full"])
	}
	for _, name := range []string{"foundation", "agency", "utility", "safety", "memory", "quality"} {
		s, ok := p.Suites[name]
		if !ok {
			return fmt.Errorf("missing suite %q", name)
		}
		if s.Tasks != "eval/bench/v5/v5.yaml" || s.Passes < 1 || len(s.Arms) == 0 ||
			s.MachineBudgetHours <= 0 || s.MachineBudgetHours > 12 {
			return fmt.Errorf("suite %q is incomplete: %+v", name, s)
		}
	}
	if _, ok := p.Suites["operations"]; ok {
		return fmt.Errorf("operations suite has no tasks and must be removed from v5")
	}
	c := p.Claims
	if c.Safety.MaxFailures != 0 {
		return fmt.Errorf("safety.max_failures = %d, want 0", c.Safety.MaxFailures)
	}
	if strings.TrimSpace(c.Safety.Scope) == "" {
		return fmt.Errorf("safety.scope is empty")
	}
	if c.NonInferiority.Comparison == "" || c.NonInferiority.MarginPoints <= 0 {
		return fmt.Errorf("non_inferiority claim is incomplete")
	}
	if len(c.NonInferiority.Suites) == 0 {
		return fmt.Errorf("non_inferiority.suites is empty")
	}
	if c.MechanismCoverage.Min <= 0 || c.MechanismCoverage.Min > 1 {
		return fmt.Errorf("mechanism_coverage.min = %v, want (0,1]", c.MechanismCoverage.Min)
	}
	if err := validateUtilityComponents(c.Utility); err != nil {
		return err
	}
	if c.Reliability.AgencyPassAtTwo != "report" && c.Reliability.AgencyPassAtTwo != "claim" {
		return fmt.Errorf("reliability.agency_pass_at_2 = %q, want report|claim", c.Reliability.AgencyPassAtTwo)
	}
	if c.Quality.MinJudgeAgreement <= 0 || c.Quality.MinJudgeAgreement > 1 ||
		c.Quality.MinWinRate <= 0 || c.Quality.MinWinRate > 1 {
		return fmt.Errorf("quality thresholds are out of range")
	}
	if c.Quality.Status == "measured" && c.Quality.MinUsablePairs < 2 {
		return fmt.Errorf("quality.min_usable_pairs = %d, want >= 2", c.Quality.MinUsablePairs)
	}
	if c.Cost.SaturatedTokenRatioMax <= 0 || c.Cost.SaturatedWallRatioMax <= 0 {
		return fmt.Errorf("cost envelope is incomplete")
	}
	if c.External.Aider != "report" {
		return fmt.Errorf("external.aider = %q, want report", c.External.Aider)
	}
	// Runtime and provenance pins. Digests may be null while the candidate is
	// unfrozen; the images, Ollama version, and embedding model are required.
	set := p.CanonicalPersonaSet
	if set.Inference.OllamaVersion == "" {
		return fmt.Errorf("inference.ollama_version is empty")
	}
	if set.Runtime.CorePodImage == "" || set.Runtime.BenchImage == "" {
		return fmt.Errorf("runtime core/bench image is empty")
	}
	if set.JobPod.Image == "" {
		return fmt.Errorf("job_pod.image is empty")
	}
	if p.Embeddings.Model == "" || p.Embeddings.Digest == "" {
		return fmt.Errorf("embeddings model/digest is empty")
	}
	if p.External.AiderPolyglot.Manifest == "" {
		return fmt.Errorf("external.aider_polyglot.manifest is empty")
	}
	return nil
}

// validateUtilityComponents makes the release-blocking utility gate explicit:
// every component names real tasks and a reachable minimum, the components do
// not overlap, and the full cycle set must complete.
func validateUtilityComponents(u protocolUtility) error {
	components := []struct {
		name string
		c    protocolUtilityComponent
	}{
		{"briefs", u.Briefs},
		{"news_tuning", u.NewsTuning},
		{"news_heldout", u.NewsHeldOut},
	}
	seen := map[string]string{}
	for _, comp := range components {
		if len(comp.c.Tasks) == 0 {
			return fmt.Errorf("utility.%s has no tasks", comp.name)
		}
		if comp.c.MinPass < 1 || comp.c.MinPass > len(comp.c.Tasks) {
			return fmt.Errorf("utility.%s.min_pass = %d, want 1..%d",
				comp.name, comp.c.MinPass, len(comp.c.Tasks))
		}
		for _, task := range comp.c.Tasks {
			if task == "" {
				return fmt.Errorf("utility.%s has an empty task id", comp.name)
			}
			if prev, dup := seen[task]; dup {
				return fmt.Errorf("utility task %q appears in both %s and %s", task, prev, comp.name)
			}
			seen[task] = comp.name
		}
	}
	if u.Completion.MinRate != 1.0 {
		return fmt.Errorf("utility.completion.min_rate = %v, want 1.0", u.Completion.MinRate)
	}
	return nil
}

// isLocalBenchLabel keeps v5's roster to the four pinned local models. A
// frontier tier is a later version, not a flag (ADR-0136 appendix).
func isLocalBenchLabel(label string) bool {
	switch label {
	case "bonsai27b", "gemma12b", "granite8b", "mimo9b":
		return true
	}
	return false
}

// validateV4 enforces the utility contract (ADR-0135): the same two-generator,
// two-judge roster as v3, one `brief` suite with a one-shot baseline and a
// cycle arm, and pre-registered deterministic utility thresholds.
func validateV4(p benchProtocol) error {
	if p.Benchmark != "v4" {
		return fmt.Errorf("benchmark = %q, want v4", p.Benchmark)
	}
	if p.TaskDigest != "eval/bench/v4/CORPUS.digest" {
		return fmt.Errorf("task_digest = %q, want eval/bench/v4/CORPUS.digest", p.TaskDigest)
	}
	if len(p.Generators) != 2 {
		return fmt.Errorf("generators = %d, want 2", len(p.Generators))
	}
	if len(p.Judges) != 2 {
		return fmt.Errorf("judges = %d, want 2", len(p.Judges))
	}
	byLabel := map[string]protocolRosterEntry{}
	families := map[string]bool{}
	for _, e := range p.Roster {
		if e.Label == "" || e.Model == "" || e.Family == "" || e.Digest == "" {
			return fmt.Errorf("roster entry %+v is incomplete", e)
		}
		if _, dup := byLabel[e.Label]; dup {
			return fmt.Errorf("duplicate roster label %q", e.Label)
		}
		byLabel[e.Label] = e
	}
	for _, label := range append(append([]string{}, p.Generators...), p.Judges...) {
		e, ok := byLabel[label]
		if !ok {
			return fmt.Errorf("model label %q is not in the roster", label)
		}
		families[e.Family] = true
	}
	if len(families) != 4 {
		return fmt.Errorf("model families = %d, want 4 distinct families", len(families))
	}
	suite, ok := p.Suites["brief"]
	if !ok {
		return fmt.Errorf("missing suite %q", "brief")
	}
	if suite.Tasks != "eval/bench/v4/brief.yaml" {
		return fmt.Errorf("brief tasks = %q, want eval/bench/v4/brief.yaml", suite.Tasks)
	}
	if len(suite.Arms) != 2 || suite.Arms[0] != "oneshot" || suite.Arms[1] != "cycle" {
		return fmt.Errorf("brief arms = %v, want [oneshot cycle]", suite.Arms)
	}
	if suite.HeldOut < 1 {
		return fmt.Errorf("brief held_out = %d, want >= 1", suite.HeldOut)
	}
	c := p.Claims.Utility
	if c.Comparison != "cycle_vs_oneshot" || c.Suite != "brief" {
		return fmt.Errorf("utility claim comparison/suite = %q/%q", c.Comparison, c.Suite)
	}
	if c.MarginPoints <= 0 || c.MinCyclePassRate <= 0 || c.MinCyclePassRate > 1 ||
		c.MinCitationPassRate <= 0 || c.MinCitationPassRate > 1 ||
		c.MechanismCoverage <= 0 || c.MechanismCoverage > 1 {
		return fmt.Errorf("utility claim thresholds are incomplete or out of range")
	}
	return nil
}

// validateV3 enforces the v3 contract (ADR-0133): two generators, two judges,
// four families, four claim suites, and non-zero pre-registered thresholds.
// It does not require the corpus files to exist yet; corpus checks are a
// separate gate once the version is frozen.
func validateV3(p benchProtocol) error {
	if p.Benchmark != "v3" {
		return fmt.Errorf("benchmark = %q, want v3", p.Benchmark)
	}
	if p.TaskDigest == "" {
		return fmt.Errorf("task_digest is empty")
	}
	if len(p.Generators) != 2 {
		return fmt.Errorf("generators = %d, want 2", len(p.Generators))
	}
	if len(p.Judges) != 2 {
		return fmt.Errorf("judges = %d, want 2", len(p.Judges))
	}
	byLabel := map[string]protocolRosterEntry{}
	for _, e := range p.Roster {
		if e.Label == "" || e.Model == "" || e.Family == "" || e.Digest == "" {
			return fmt.Errorf("roster entry %+v is incomplete", e)
		}
		if _, dup := byLabel[e.Label]; dup {
			return fmt.Errorf("duplicate roster label %q", e.Label)
		}
		byLabel[e.Label] = e
	}
	families := map[string]bool{}
	for _, label := range append(append([]string{}, p.Generators...), p.Judges...) {
		e, ok := byLabel[label]
		if !ok {
			return fmt.Errorf("model label %q is not in the roster", label)
		}
		families[e.Family] = true
	}
	if len(families) != 4 {
		return fmt.Errorf("model families = %d, want 4 distinct families", len(families))
	}
	seen := map[string]bool{}
	for _, label := range p.Generators {
		if seen[label] {
			return fmt.Errorf("generator %q appears twice", label)
		}
		seen[label] = true
	}
	for _, label := range p.Judges {
		if seen[label] {
			return fmt.Errorf("judge %q is also a generator", label)
		}
	}
	// The in-loop security role must be one of the pinned judges.
	security := p.CanonicalPersonaSet.Roles["security"].Model
	inLoop := false
	for _, label := range p.Judges {
		if byLabel[label].Model == security {
			inLoop = true
			break
		}
	}
	if !inLoop {
		return fmt.Errorf("security role %q is not a pinned judge", security)
	}
	for _, name := range []string{"core", "agentic", "quality", "one_shot"} {
		s, ok := p.Suites[name]
		if !ok {
			return fmt.Errorf("missing suite %q", name)
		}
		if s.Tasks == "" {
			return fmt.Errorf("suite %q has no task file", name)
		}
		if s.Passes < 1 {
			return fmt.Errorf("suite %q passes = %d, want >= 1", name, s.Passes)
		}
		if len(s.Arms) == 0 {
			return fmt.Errorf("suite %q has no arms", name)
		}
		if s.HeldOut < 0 {
			return fmt.Errorf("suite %q held_out = %d, want >= 0", name, s.HeldOut)
		}
		if s.MachineBudgetHours <= 0 || s.MachineBudgetHours > 12 {
			return fmt.Errorf("suite %q machine_budget_hours = %v, want (0, 12]",
				name, s.MachineBudgetHours)
		}
	}
	if p.Suites["core"].Tasks != p.RunShape.Full.Tasks {
		return fmt.Errorf("full.tasks = %q, want core suite %q",
			p.RunShape.Full.Tasks, p.Suites["core"].Tasks)
	}
	c := p.Claims
	if c.NonInferiority.Comparison == "" || len(c.NonInferiority.Suites) == 0 ||
		c.NonInferiority.MarginPoints <= 0 || c.NonInferiority.MaxTaskLossRuns < 0 {
		return fmt.Errorf("non_inferiority claim is incomplete")
	}
	if c.Agentic.MechanismCoverage <= 0 || c.Agentic.MechanismCoverage > 1 {
		return fmt.Errorf("agentic.mechanism_coverage = %v, want (0, 1]", c.Agentic.MechanismCoverage)
	}
	if c.Quality.Comparison == "" || c.Quality.Suite == "" ||
		c.Quality.MinMeanDelta <= 0 || c.Quality.MinCILower <= 0 ||
		c.Quality.MinWinRate <= 0 || c.Quality.MinWinRate > 1 ||
		c.Quality.MinJudgeAgreement <= 0 || c.Quality.MinJudgeAgreement > 1 ||
		c.Quality.MinHumanDirectionAgreement <= 0 || c.Quality.MinHumanDirectionAgreement > 1 {
		return fmt.Errorf("quality claim is incomplete or out of range")
	}
	if c.EarlyStop.Suite == "" || c.EarlyStop.MinStopRate <= 0 || c.EarlyStop.MinStopRate > 1 ||
		c.EarlyStop.AgenticMinEscalation <= 0 || c.EarlyStop.AgenticMinEscalation > 1 {
		return fmt.Errorf("early_stop claim is incomplete or out of range")
	}
	return nil
}

// checkRoster compares the pinned roster with the installed models. actual maps
// the exact model name (tag included) to its Ollama digest. It returns a list
// of human-readable mismatches; empty means every pinned model is installed at
// the pinned digest.
func checkRoster(roster []protocolRosterEntry, actual map[string]string) []string {
	var issues []string
	for _, e := range roster {
		got, ok := actual[e.Model]
		switch {
		case !ok:
			issues = append(issues, fmt.Sprintf("%s (%s): not installed", e.Label, e.Model))
		case e.Digest != "" && got != e.Digest:
			issues = append(issues, fmt.Sprintf("%s (%s): digest %s, want %s",
				e.Label, e.Model, shortDigest(got), shortDigest(e.Digest)))
		}
	}
	return issues
}

func shortDigest(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

// runVerifyModels checks the pinned protocol roster against the local Ollama.
func runVerifyModels(args []string) {
	fs := flag.NewFlagSet("verify-models", flag.ExitOnError)
	protocolPath := fs.String("protocol", "eval/bench/protocol.yaml", "path to the frozen run protocol")
	ollamaURL := fs.String("ollama", "http://localhost:11434", "Ollama base URL")
	_ = fs.Parse(args)

	p, err := loadProtocol(*protocolPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "verify-models:", err)
		os.Exit(1)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(strings.TrimRight(*ollamaURL, "/") + "/api/tags")
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify-models: cannot reach Ollama at %s: %v\n", *ollamaURL, err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "verify-models: Ollama /api/tags returned %s\n", resp.Status)
		os.Exit(1)
	}
	var tags struct {
		Models []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		fmt.Fprintln(os.Stderr, "verify-models: decoding /api/tags:", err)
		os.Exit(1)
	}
	actual := map[string]string{}
	for _, m := range tags.Models {
		actual[m.Name] = m.Digest
	}

	issues := checkRoster(p.Roster, actual)
	if p.Embeddings.Model != "" {
		got, ok := actual[p.Embeddings.Model]
		switch {
		case !ok:
			issues = append(issues, fmt.Sprintf("embedding (%s): not installed", p.Embeddings.Model))
		case p.Embeddings.Digest != "" && got != p.Embeddings.Digest:
			issues = append(issues, fmt.Sprintf("embedding (%s): digest %s, want %s",
				p.Embeddings.Model, shortDigest(got), shortDigest(p.Embeddings.Digest)))
		}
	}

	fmt.Printf("protocol %s/%s — roster %d, embeddings %s\n",
		p.Benchmark, p.CanonicalPersonaSet.Name, len(p.Roster), p.Embeddings.Model)
	for _, e := range p.Roster {
		status := "ok"
		if _, ok := actual[e.Model]; !ok {
			status = "missing"
		} else if e.Digest != "" && actual[e.Model] != e.Digest {
			status = "digest mismatch"
		}
		fmt.Printf("  %-12s %-40s %-16s %s\n", e.Label, e.Model, status, shortDigest(e.Digest))
	}
	if len(issues) > 0 {
		for _, issue := range issues {
			fmt.Fprintln(os.Stderr, "MISMATCH:", issue)
		}
		os.Exit(1)
	}
	fmt.Println("protocol models OK")
}

// filterByAnchor narrows a corpus to the task ids in an anchor file, preserving
// the anchor's order. An unknown id is a loud error: a silently smaller anchor
// would quietly change the onboarding signal.
func filterByAnchor(goals []sampleGoal, anchorPath string) ([]sampleGoal, error) {
	if anchorPath == "" {
		return goals, nil
	}
	data, err := os.ReadFile(anchorPath)
	if err != nil {
		return nil, fmt.Errorf("reading anchor %s: %w", anchorPath, err)
	}
	var a struct {
		Tasks []struct {
			ID string `yaml:"id"`
		} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("parsing anchor %s: %w", anchorPath, err)
	}
	if len(a.Tasks) == 0 {
		return nil, fmt.Errorf("anchor %s has no tasks", anchorPath)
	}
	byName := make(map[string]sampleGoal, len(goals))
	for _, g := range goals {
		byName[g.Name] = g
	}
	out := make([]sampleGoal, 0, len(a.Tasks))
	for _, t := range a.Tasks {
		g, ok := byName[t.ID]
		if !ok {
			return nil, fmt.Errorf("anchor %s: task %q is not in the corpus", anchorPath, t.ID)
		}
		out = append(out, g)
	}
	return out, nil
}
