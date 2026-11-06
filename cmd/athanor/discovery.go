package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/tcs76321/athanor/internal/composition"
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
	pool   *composition.Pool
}

func (g *discoveryGenerator) Generate(ctx context.Context, req discovery.Request) (string, error) {
	if g.client == nil {
		return "", fmt.Errorf("discovery: no inference client")
	}
	model, contextTarget, err := pickWorkModel(g.pool, g.cfg, "discovery", composition.StanceSynthesize, 16384)
	if err != nil {
		return "", fmt.Errorf("discovery: %w", err)
	}
	maxTokens := g.cfg.Inference.MaxOutputTokens
	// A discovery portfolio is ten rich objects with ~20 fields each. The
	// general 4096-token runaway guard truncates that JSON before the closing
	// array and produces "response contains no JSON array"; discovery needs a
	// larger, still bounded budget.
	const discoveryMinOutputTokens = 12288
	if maxTokens < discoveryMinOutputTokens {
		maxTokens = discoveryMinOutputTokens
	}
	prompt := discovery.BuildPrompt(req)
	req.Report(discovery.ProgressEvent{
		Stage: "prompt", Detail: "prompt assembled for " + model, Bytes: len(prompt),
	})
	req.Report(discovery.ProgressEvent{
		Stage: "model", Detail: "waiting for " + model, Bytes: len(prompt),
	})

	var streamed int
	var tail string
	resp, err := g.client.Stream(ctx, llm.Request{
		Model:         model,
		Messages:      []llm.Message{{Role: "user", Content: prompt}},
		Temperature:   0.8,
		ContextTarget: contextTarget,
		MaxTokens:     maxTokens,
		Think:         llm.Off(),
		Format: map[string]any{
			"type":     "array",
			"minItems": 6,
			"items":    map[string]any{"type": "object"},
		},
	}, func(tok string) {
		streamed += len(tok)
		tail += tok
		if len(tail) > 1200 {
			tail = tail[len(tail)-1200:]
		}
		if streamed%1024 < len(tok) {
			req.Report(discovery.ProgressEvent{
				Stage: "streaming", Detail: "receiving model output", Bytes: streamed, Tail: tail,
			})
		}
	})
	if err != nil {
		return "", fmt.Errorf("discovery: model call: %w", err)
	}
	if strings.TrimSpace(resp.Content) == "" {
		if resp.Thinking != "" {
			return "", fmt.Errorf("discovery: model returned only hidden reasoning (%d chars); thinking is disabled for discovery", len(resp.Thinking))
		}
		return "", fmt.Errorf("discovery: model returned an empty response")
	}
	req.Report(discovery.ProgressEvent{
		Stage: "response", Detail: "model response complete", Bytes: len(resp.Content), Tail: tail,
	})
	return resp.Content, nil
}
