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
	model, contextTarget := req.Model, req.ContextTarget
	if model == "" {
		var err error
		model, contextTarget, err = pickWorkModel(g.pool, g.cfg, "planning", composition.StancePlan, 8192)
		if err != nil {
			return "", fmt.Errorf("planning: %w", err)
		}
	} else if contextTarget <= 0 {
		contextTarget = 8192
	}
	maxTokens := g.cfg.Inference.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	prompt := planning.BuildPrompt(req)
	req.Report(planning.ProgressEvent{
		Phase: req.Phase, Stage: "prompt", Detail: "prompt assembled for " + model,
		Model: model, ContextTarget: contextTarget, PromptBytes: len(prompt),
	})
	req.Report(planning.ProgressEvent{
		Phase: req.Phase, Stage: "model", Detail: "waiting for " + model,
		Model: model, ContextTarget: contextTarget,
	})
	var streamed int
	var tail string
	resp, err := g.client.Stream(ctx, llm.Request{
		Model:         model,
		Messages:      []llm.Message{{Role: "user", Content: prompt}},
		Temperature:   0.4,
		ContextTarget: contextTarget,
		MaxTokens:     maxTokens,
		Format:        "json",
	}, func(tok string) {
		streamed += len(tok)
		tail += tok
		if len(tail) > 1200 {
			tail = tail[len(tail)-1200:]
		}
		if streamed%1024 < len(tok) {
			req.Report(planning.ProgressEvent{
				Phase: req.Phase, Stage: "streaming", Detail: "receiving model output",
				Model: model, ContextTarget: contextTarget, ResponseBytes: streamed, Tail: tail,
			})
		}
	})
	if err != nil {
		return "", fmt.Errorf("planning: model call: %w", err)
	}
	req.Report(planning.ProgressEvent{
		Phase: req.Phase, Stage: "response", Detail: "model response received",
		Model: model, ContextTarget: contextTarget, ResponseBytes: len(resp.Content),
		Tail: tail, Tokens: resp.PromptTokens + resp.CompletionTokens,
	})
	return resp.Content, nil
}
