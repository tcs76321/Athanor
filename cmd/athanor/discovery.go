package main

import (
	"context"
	"fmt"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/discovery"
	"github.com/tcs76321/athanor/internal/llm"
)

// discoveryGenerator adapts llm.Client to discovery.Generator. The prompt and
// validation rules live in internal/discovery; only the provider call lives
// here, keeping internal/ free of provider-specific wiring.
type discoveryGenerator struct {
	client *llm.Client
	cfg    *config.Config
}

func (g *discoveryGenerator) Generate(ctx context.Context, req discovery.Request) (string, error) {
	if g.client == nil {
		return "", fmt.Errorf("discovery: no inference client")
	}
	pc, ok := g.cfg.Personas.Role("tall")
	if !ok {
		pc, ok = g.cfg.Personas.Role("main")
	}
	if !ok {
		return "", fmt.Errorf("discovery: no tall/main persona configured")
	}
	maxTokens := g.cfg.Inference.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	resp, err := g.client.Chat(ctx, llm.Request{
		Model:         pc.Model,
		Messages:      []llm.Message{{Role: "user", Content: discovery.BuildPrompt(req)}},
		Temperature:   0.8,
		ContextTarget: pc.ContextTarget,
		MaxTokens:     maxTokens,
		Format:        "json",
	})
	if err != nil {
		return "", fmt.Errorf("discovery: model call: %w", err)
	}
	return resp.Content, nil
}
