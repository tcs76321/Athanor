package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Embedding calls (ROADMAP M5-T7; ADR-0026 §3).
//
// Ollama's embedding endpoint is a separate route from /api/chat: the
// request carries a model and one or more input strings, and the response
// carries one vector per input. This file is the only place that knows that
// wire shape — the MCE talks to an `mce.Embedder` seam, whose adapter lives
// in cmd/ (ADR-0026 §3), so `internal/mce` never imports `internal/llm`.

// ErrEmptyEmbedInput reports an embedding request with no texts. An empty
// input list is a caller bug, not a model condition.
var ErrEmptyEmbedInput = errors.New("llm: embedding request has no input")

// embedRequest/embedResponse mirror Ollama's /api/embed schema.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed returns one embedding vector per input text, in order. Transport
// failures are wrapped in ErrUnreachable, matching Chat; a response with the
// wrong number of vectors is rejected rather than silently misaligned.
func (c *Client) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if model == "" {
		return nil, fmt.Errorf("llm: embedding request has no model")
	}
	if len(texts) == 0 {
		return nil, ErrEmptyEmbedInput
	}
	body, err := json.Marshal(embedRequest{Model: model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("llm: marshalling embedding request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm: building embedding request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("llm: embed call canceled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("%w: %s: %v", ErrUnreachable, c.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Embedding responses can be large (one float per dimension per input);
	// 8 MiB is a generous guard against a runaway body.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("llm: reading embedding response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm: ollama embed returned %s: %s", resp.Status, snippet(raw))
	}

	var er embedResponse
	if err := json.Unmarshal(raw, &er); err != nil {
		return nil, fmt.Errorf("llm: decoding embedding response: %w", err)
	}
	if len(er.Embeddings) != len(texts) {
		return nil, fmt.Errorf("llm: embed returned %d vectors for %d inputs", len(er.Embeddings), len(texts))
	}
	return er.Embeddings, nil
}
