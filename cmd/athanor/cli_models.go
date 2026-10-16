package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/onboard"
)

// modelReserveBytes is the RAM held back from model-fit decisions: Ollama, the
// KV cache, the daemon, and the OS need headroom beyond a model's weights.
const modelReserveBytes = 2 << 30

// modelRoleNames is the fixed persona role set, in display order.
var modelRoleNames = []string{"wide", "tall", "main", "security", "alternative"}

// runModels implements `athanor models` (M9-T2, ADR-0049): it presents the
// per-role onboarding inventory — is each configured persona's model installed,
// and which installed models also fit in memory — and, when a qualification
// measurements file is supplied, the advisory routing proposal from
// onboard.Route.
//
// Applying a recommendation is a config edit the operator makes; the output is
// advisory and never mutates config (the HITL gate is the human). Qualification
// measurements come from the probe (docs/qualification-smoke-m9.md).
func runModels(args []string) error {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "path to config.yaml")
	qualPath := fs.String("qualification", "", "optional JSON file of per-role qualification measurements")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	probes := newOSProbes()
	ctx := context.Background()

	models, err := ollamaCatalog(ctx, probes, cfg.Inference.OllamaURL)
	if err != nil {
		return fmt.Errorf("listing installed models from %s: %w", cfg.Inference.OllamaURL, err)
	}

	var measurements []onboard.Measurement
	if *qualPath != "" {
		raw, err := os.ReadFile(*qualPath)
		if err != nil {
			return fmt.Errorf("reading qualification file: %w", err)
		}
		if err := json.Unmarshal(raw, &measurements); err != nil {
			return fmt.Errorf("parsing qualification file %s: %w", *qualPath, err)
		}
	}

	mem, _ := probes.TotalMemoryBytes(ctx)
	roles, families := modelRoles(cfg, models)
	got := onboard.Assess(roles, families, models,
		int64(mem), modelReserveBytes, measurements, modelRoutingOptions())

	printModelAssessment(cfg, got, *qualPath != "")
	return nil
}

// ollamaCatalog reads the installed models with their sizes and Ollama's own
// family hint from /api/tags. The hint is a fallback; a configured model's
// operator-declared family (config) overrides it in modelRoles.
func ollamaCatalog(ctx context.Context, probes *osProbes, baseURL string) ([]onboard.Model, error) {
	var body struct {
		Models []struct {
			Name    string `json:"name"`
			Model   string `json:"model"`
			Size    int64  `json:"size"`
			Details struct {
				Family string `json:"family"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := probes.getJSON(ctx, strings.TrimRight(baseURL, "/")+"/api/tags", &body); err != nil {
		return nil, err
	}
	out := make([]onboard.Model, 0, len(body.Models))
	for _, m := range body.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		if name == "" {
			continue
		}
		out = append(out, onboard.Model{Name: name, SizeBytes: m.Size, Family: m.Details.Family})
	}
	return out, nil
}

// modelRoles builds the onboarding roles and the model→family map from config,
// overlaying the operator-declared family over the catalog hint.
func modelRoles(cfg *config.Config, models []onboard.Model) ([]onboard.Role, map[string]string) {
	families := map[string]string{}
	for _, m := range models {
		if m.Family != "" {
			families[m.Name] = m.Family
		}
	}
	var roles []onboard.Role
	for _, name := range modelRoleNames {
		pc, ok := cfg.Personas.Role(name)
		if !ok {
			continue
		}
		roles = append(roles, onboard.Role{Role: name, Model: pc.Model, ContextTarget: pc.ContextTarget})
		if pc.Model != "" && pc.Family != "" {
			families[pc.Model] = pc.Family
		}
	}
	return roles, families
}

// modelRoutingOptions is the conservative default the engine uses: the judge
// (security) must differ from the generator roles, and a candidate needs more
// than one noisy sample to displace the configured model.
func modelRoutingOptions() onboard.RoutingOptions {
	return onboard.RoutingOptions{
		JudgeRole:      "security",
		GeneratorRoles: []string{"main", "tall", "alternative", "wide"},
		MinImprovement: 0.05,
		MinSamples:     2,
	}
}

func printModelAssessment(cfg *config.Config, a onboard.Assessment, withQualification bool) {
	fmt.Printf("athanor models — installed catalog from %s\n", cfg.Inference.OllamaURL)
	fmt.Printf("memory: %.1f GiB total, %.1f GiB reserved for the OS/daemon/KV cache\n\n",
		float64(a.Report.AvailableBytes)/(1<<30), float64(a.Report.ReserveBytes)/(1<<30))
	fmt.Printf("%-12s %-24s %-10s %s\n", "ROLE", "CONFIGURED", "INSTALLED", "ALTERNATIVES (fit)")
	for _, pr := range a.Report.Personas {
		installed := "yes"
		if !pr.Installed {
			installed = "MISSING"
		}
		var alts []string
		for _, m := range pr.Alternatives {
			alts = append(alts, m.Name)
		}
		fmt.Printf("%-12s %-24s %-10s %s\n", pr.Role, pr.Configured, installed, strings.Join(alts, ", "))
	}
	fmt.Println()
	if !withQualification {
		fmt.Println("routing: no qualification data supplied — pass -qualification <file> to propose a table")
		fmt.Println("         (qualify with the probe; see docs/qualification-smoke-m9.md)")
	}
	fmt.Println("routing proposal (advisory — apply by editing config.yaml; ADR-0049):")
	for _, r := range a.Recommendations {
		action := "keep"
		if r.Replace {
			action = "-> " + r.Recommended
		}
		fmt.Printf("  %-12s %-24s %-6s %s\n", r.Role, r.Configured, action, r.Reason)
	}
}
