package main

import (
	"context"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/llm"
)

// assistGenerator is the interactive prompt-editor model call. It prefers the
// measured pool (quality over speed for an explicit user action) and falls
// back to the tall persona when no pool is available.
type assistGenerator struct {
	client   *llm.Client
	selector *composition.Selector
	fallback llm.Persona
}

func (g *assistGenerator) Generate(ctx context.Context, system, user string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	model := g.fallback.Model
	family := g.fallback.Family
	contextTarget := g.fallback.ContextTarget
	if g.selector != nil && g.selector.Pool != nil {
		if c, ok := g.selector.Pool.Choose(composition.Request{
			Phase: "assist", Capability: composition.CapabilityGeneral,
			Stance: composition.StanceSynthesize, MinContext: 8192, PreferQuality: true,
		}); ok && c.Model != "" {
			model = c.Model
			family = c.Family
			contextTarget = c.ContextTarget
		}
	}
	if model == "" {
		return "", fmt.Errorf("no model is available for field refinement")
	}
	if contextTarget < 8192 {
		contextTarget = 8192
	}
	maxTokens := 1800
	if len(user) > 4000 {
		maxTokens = 4000
	}
	resp, err := g.client.Chat(ctx, llm.Request{
		Model: model,
		Messages: []llm.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature:   0.2,
		ContextTarget: contextTarget,
		Format:        "json",
		MaxTokens:     maxTokens,
		Think:         llm.Off(),
	})
	if err != nil {
		return "", fmt.Errorf("refinement model (%s, %s): %w", model, family, err)
	}
	return resp.Content, nil
}
