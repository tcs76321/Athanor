package main

import (
	"context"

	"github.com/tcs76321/athanor/internal/llm"
)

// llmDaydreamGenerator adapts llm.Client to the daydream generator seam using
// the `main` persona. Output is bounded and thinking is off so the draft is the
// visible content (the M3-T7 runaway guard, §17.2 draft-only).
type llmDaydreamGenerator struct {
	client        *llm.Client
	model         string
	contextTarget int
	temperature   float64
}

// Generate performs one bounded generation for Proactive Documentation.
func (g llmDaydreamGenerator) Generate(ctx context.Context, prompt string) (string, error) {
	off := false
	resp, err := g.client.Chat(ctx, llm.Request{
		Model:         g.model,
		Messages:      []llm.Message{{Role: "user", Content: prompt}},
		Temperature:   g.temperature,
		ContextTarget: g.contextTarget,
		MaxTokens:     2048,
		Think:         &off,
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// mainPersonaGenerator resolves the `main` persona into a generator. A missing
// role yields a nil seam (the action is disabled).
func mainPersonaGenerator(registry *llm.Registry, client *llm.Client) daydreamGenerator {
	if registry == nil || client == nil {
		return nil
	}
	p, ok := registry.Persona(llm.RoleMain)
	if !ok {
		return nil
	}
	return llmDaydreamGenerator{
		client: client, model: p.Model, contextTarget: p.ContextTarget, temperature: p.Temperature,
	}
}
