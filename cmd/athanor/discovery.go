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
	prompt := discovery.BuildPrompt(req)
	req.Report(discovery.ProgressEvent{
		Stage: "prompt", Detail: "prompt assembled for " + pc.Model, Bytes: len(prompt),
	})
	req.Report(discovery.ProgressEvent{
		Stage: "model", Detail: "waiting for " + pc.Model, Bytes: len(prompt),
	})

	var streamed int
	var tail string
	resp, err := g.client.Stream(ctx, llm.Request{
		Model:         pc.Model,
		Messages:      []llm.Message{{Role: "user", Content: prompt}},
		Temperature:   0.8,
		ContextTarget: pc.ContextTarget,
		MaxTokens:     maxTokens,
		Format:        "json",
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
	req.Report(discovery.ProgressEvent{
		Stage: "response", Detail: "model response complete", Bytes: len(resp.Content), Tail: tail,
	})
	return resp.Content, nil
}
