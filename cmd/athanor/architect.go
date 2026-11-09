package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/llm"
)

// architectGenerator is the production Completer for the conversation-first
// onboarding. It is a planning call, not an engine phase: JSON only, no tools,
// and a bounded timeout so a slow local model cannot stall the first screen.
type architectGenerator struct {
	client   *llm.Client
	selector *composition.Selector
	fallback llm.Persona
}

func (g *architectGenerator) Complete(ctx context.Context, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	model := g.fallback.Model
	family := g.fallback.Family
	contextTarget := g.fallback.ContextTarget
	if g.selector != nil && g.selector.Pool != nil && poolHasScores(g.selector.Pool) {
		if c, ok := g.selector.Pool.Choose(composition.Request{
			Phase: "architect", Capability: composition.CapabilityInstruction,
			Stance: composition.StanceSynthesize, MinContext: 16384, PreferQuality: true,
		}); ok && c.Model != "" {
			model, family, contextTarget = c.Model, c.Family, c.ContextTarget
		}
	}
	if model == "" {
		return "", fmt.Errorf("architect: no model is available for project framing")
	}
	if contextTarget < 16384 {
		contextTarget = 16384
	}
	resp, err := g.client.Chat(ctx, llm.Request{
		Model: model,
		Messages: []llm.Message{
			{Role: "system", Content: "You are Athanor's project architect. Return only the requested JSON object."},
			{Role: "user", Content: prompt},
		},
		Temperature:   0.3,
		ContextTarget: contextTarget,
		Format:        "json",
		MaxTokens:     3000,
		Think:         llm.Off(),
	})
	if err != nil {
		return "", fmt.Errorf("architect model (%s, %s): %w", model, family, err)
	}
	slog.Debug("architect: model response", "model", model, "family", family,
		"prompt_bytes", len(prompt), "response_bytes", len(resp.Content),
		"has_json", strings.Contains(resp.Content, "{"))
	return resp.Content, nil
}
