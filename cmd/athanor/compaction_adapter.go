package main

import (
	"context"
	"fmt"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/prompt"
)

// mceCompactor adapts the `security` persona to mce.Compactor (ADR-0025 §5).
// It is the only place a compaction touches a model — the MCE package itself
// stays free of internal/llm, preserving Gate G2 rule 1's intent. Every call is
// pinned to temperature 0.0 (§10.3): compaction is not a §13.1 phase, so
// ResolveTemperature is never consulted.
type mceCompactor struct {
	registry *llm.Registry
	client   *llm.Client
}

// Compile-time proof that the adapter satisfies the MCE seam.
var _ mce.Compactor = (*mceCompactor)(nil)

// newMCECompactor builds the adapter and re-asserts the §10.3 invariant that
// config validation already enforces, so a programming error cannot silently
// bypass it.
func newMCECompactor(registry *llm.Registry, client *llm.Client, ce config.ContextEngine) (*mceCompactor, error) {
	if ce.CompactionTemperature != mce.CompactionTemperature {
		return nil, fmt.Errorf("mce compactor: context_engine.compaction_temperature must be %v, got %v",
			mce.CompactionTemperature, ce.CompactionTemperature)
	}
	return &mceCompactor{registry: registry, client: client}, nil
}

// Compact requests one compaction at temp 0.0 on the security persona. The
// model's output is returned verbatim — nothing is truncated (ADR-0025 §7).
func (c *mceCompactor) Compact(ctx context.Context, item mce.MemoryItem, kind mce.CompactionKind) (string, error) {
	persona, ok := c.registry.Persona(llm.RoleSecurity)
	if !ok {
		return "", fmt.Errorf("mce compactor: persona %q missing from registry", llm.RoleSecurity)
	}
	profileType := string(item.Profile.Type)
	profileState := string(item.Profile.State)
	var messages []llm.Message
	switch kind {
	case mce.CompactionDeterministic:
		messages = prompt.CompactDeterministicMessages(profileType, profileState, item.Ref.RelPath, string(item.Content))
	case mce.CompactionSemantic:
		messages = prompt.CompactSemanticMessages(profileType, profileState, item.Ref.RelPath, string(item.Content))
	default:
		return "", fmt.Errorf("mce compactor: unknown compaction kind %q", kind)
	}
	resp, err := c.client.Chat(ctx, llm.Request{
		Model:         persona.Model,
		Messages:      messages,
		Temperature:   mce.CompactionTemperature,
		ContextTarget: persona.ContextTarget,
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// TemplateVersion returns the versioned prompt name for a kind. It is part of
// the content-address key (ADR-0025 §1), so a template change is a new row.
func (c *mceCompactor) TemplateVersion(kind mce.CompactionKind) string {
	switch kind {
	case mce.CompactionDeterministic:
		return prompt.CompactDeterministicVersion
	case mce.CompactionSemantic:
		return prompt.CompactSemanticVersion
	default:
		return ""
	}
}
