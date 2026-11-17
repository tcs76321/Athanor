package firstuse

import (
	"context"
	"strings"

	"github.com/tcs76321/athanor/internal/coverage"
	"github.com/tcs76321/athanor/internal/workflow"
)

// injectCoverage adds the recurring-cycle no-repeat check to a graph at
// compile time: a synthesize block that declares `coverage_namespace` gets the
// recent keys from that ledger as forbidden ids under its New section. The
// block can still declare its own checks; this only adds the dynamic rule.
func injectCoverage(ctx context.Context, repo *coverage.Repo, projectID string, g workflow.Graph) workflow.Graph {
	if repo == nil {
		return g
	}
	for i := range g.Blocks {
		b := &g.Blocks[i]
		if b.Kind != workflow.KindSynthesize {
			continue
		}
		ns := configString(b.Config["coverage_namespace"])
		if ns == "" {
			continue
		}
		keys, err := repo.Keys(ctx, projectID, ns, 50)
		if err != nil || len(keys) == 0 {
			continue
		}
		if b.Config == nil {
			b.Config = map[string]any{}
		}
		checks, _ := b.Config["checks"].(map[string]any)
		if checks == nil {
			checks = map[string]any{}
			b.Config["checks"] = checks
		}
		addForbiddenIDs(checks, "New", keys)
	}
	return g
}

// addForbiddenIDs appends keys to the checks.forbidden_ids_in_sections rule
// for one section, merging with an existing rule in the config.
func addForbiddenIDs(checks map[string]any, section string, keys []string) {
	rules, _ := checks["forbidden_ids_in_sections"].([]any)
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if configString(rule["section"]) != section {
			continue
		}
		existing, _ := rule["ids"].([]string)
		if len(existing) == 0 {
			if list, ok := rule["ids"].([]any); ok {
				for _, x := range list {
					if s, ok := x.(string); ok {
						existing = append(existing, s)
					}
				}
			}
		}
		rule["ids"] = append(existing, keys...)
		return
	}
	checks["forbidden_ids_in_sections"] = append(rules, map[string]any{
		"section": section, "ids": keys,
	})
}

func configString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
