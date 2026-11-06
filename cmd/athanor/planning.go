package main

import (
	"context"
	"fmt"

	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/planning"
)

// planningGenerator adapts llm.Client to planning.Generator. The prompt and
// validation live in internal/planning; only the provider call lives here.
type planningGenerator struct {
	client *llm.Client
	cfg    *config.Config
	pool   *composition.Pool
}

func (g *planningGenerator) Generate(ctx context.Context, req planning.Request) (string, error) {
	if g.client == nil {
		return "", fmt.Errorf("planning: no inference client")
	}
	model, contextTarget, err := pickWorkModel(g.pool, g.cfg, "planning", composition.StancePlan, 8192)
	if err != nil {
		return "", fmt.Errorf("planning: %w", err)
	}
	maxTokens := g.cfg.Inference.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	resp, err := g.client.Chat(ctx, llm.Request{
		Model:         model,
		Messages:      []llm.Message{{Role: "user", Content: planning.BuildPrompt(req)}},
		Temperature:   0.4,
		ContextTarget: contextTarget,
		MaxTokens:     maxTokens,
		Format:        "json",
	})
	if err != nil {
		return "", fmt.Errorf("planning: model call: %w", err)
	}
	return resp.Content, nil
}
