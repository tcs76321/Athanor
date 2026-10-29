package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/store"
)

// TestCaptureLLMPayloads proves the opt-in inspector capture: when enabled,
// an llm_call event carries the exact prompt messages and visible response.
func TestCaptureLLMPayloads(t *testing.T) {
	env := newEnvWithCfg(t, func(c *config.Config) {
		c.Logging.CaptureLLMPayloads = true
	})
	jobID := env.submit(t)
	env.eng.Run(context.Background(), jobID)

	events, err := env.db.QueryEvents(context.Background(),
		store.EventFilter{JobID: jobID, Category: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if strings.Contains(e.DataJSON, `"prompt_messages"`) &&
			strings.Contains(e.DataJSON, `"response"`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no llm_call event carried prompt_messages/response")
	}
}

// TestCaptureLLMPayloadsOffByDefault pins the privacy default: counts only.
func TestCaptureLLMPayloadsOffByDefault(t *testing.T) {
	env := newEnv(t)
	jobID := env.submit(t)
	env.eng.Run(context.Background(), jobID)

	events, err := env.db.QueryEvents(context.Background(),
		store.EventFilter{JobID: jobID, Category: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if strings.Contains(e.DataJSON, `"prompt_messages"`) {
			t.Fatal("prompt payload captured with capture_llm_payloads off")
		}
	}
}
