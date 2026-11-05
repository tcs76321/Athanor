package main

import (
	"context"
	"fmt"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/planning"
)

// planningGenerator adapts llm.Client to planning.Generator. The prompt and
// validation live in internal/planning; only the provider call lives here.
type planningGenerator struct {
	client *llm.Client
	cfg    *config.Config
}

func (g *planningGenerator) Generate(ctx context.Context, req planning.Request) (string, error) {
	if g.client == nil {
		return "", fmt.Errorf("planning: no inference client")
	}
	pc, ok := g.cfg.Personas.Role("tall")
	if !ok {
		pc, ok = g.cfg.Personas.Role("main")
	}
	if !ok {
		return "", fmt.Errorf("planning: no tall/main persona configured")
	}
	maxTokens := g.cfg.Inference.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	resp, err := g.client.Chat(ctx, llm.Request{
		Model:         pc.Model,
		Messages:      []llm.Message{{Role: "user", Content: planning.BuildPrompt(req)}},
		Temperature:   0.4,
		ContextTarget: pc.ContextTarget,
		MaxTokens:     maxTokens,
		Format:        "json",
	})
	if err != nil {
		return "", fmt.Errorf("planning: model call: %w", err)
	}
	return resp.Content, nil
}
