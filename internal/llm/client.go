package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrUnreachable reports that Ollama could not be reached at all (§ M1-T1
// acceptance: unreachable Ollama fails loudly, with a typed error callers
// can branch on to pause jobs rather than retry blindly).
var ErrUnreachable = errors.New("ollama unreachable")

// defaultTimeout bounds one non-streaming chat call. Local models on slow
// machines can take minutes for long generations; the per-phase wall-time
// budgets (§8.2) provide the tighter bound, this is the outer guard.
const defaultTimeout = 10 * time.Minute

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"` // "system" | "user" | "assistant"
	Content string `json:"content"`
}

// Request is one model call resolved for a persona.
type Request struct {
	Model       string
	Messages    []Message
	Temperature float64
	// ContextTarget is the num_ctx the model is loaded with (§12.3: set at
	// model load time, per persona).
	ContextTarget int
	// Seed pins Ollama's sampler seed when non-nil (M3-T7.1). A nil
	// pointer leaves the seed unset, so Ollama draws a fresh random seed
	// per request. Structured judgment calls at Temperature 0.0 may set
	// it for reproducibility and audit; the divergence phase must NOT
	// set it — a shared seed would collapse the N candidates into
	// near-duplicates and destroy the value of multiple candidates.
	Seed *int64
	// Format requests a constrained response format: either the string
	// "json" or a JSON-schema object (M3-T7.5b). It is set only for the
	// structured judgment phases (evaluating, comparing); nil leaves it
	// unset.
	Format any
}

// Response carries the model reply plus token accounting (§28.2).
type Response struct {
	Content          string
	PromptTokens     int // Ollama prompt_eval_count
	CompletionTokens int // Ollama eval_count
}

// Client talks to one Ollama server over its native HTTP API.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient returns a client for the Ollama server at baseURL (e.g.
// cfg.Inference.OllamaURL). A nil httpClient selects a sane default.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

// chatRequest/chatResponse mirror Ollama's /api/chat schema. Options
// carry the persona's load-time knobs (§12.3).
type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
	Format   any       `json:"format,omitempty"`
	Options  chatOpts  `json:"options"`
}

type chatOpts struct {
	Temperature float64 `json:"temperature"`
	NumCtx      int     `json:"num_ctx"`
	Seed        *int64  `json:"seed,omitempty"`
}

type chatResponse struct {
	Message         Message `json:"message"`
	Done            bool    `json:"done"`
	PromptEvalCount int     `json:"prompt_eval_count"`
	EvalCount       int     `json:"eval_count"`
}

// Chat performs one non-streaming chat completion. The context bounds the
// call; transport-level failures are wrapped in ErrUnreachable.
func (c *Client) Chat(ctx context.Context, req Request) (Response, error) {
	if req.Model == "" {
		return Response{}, fmt.Errorf("llm: request has no model")
	}
	body, err := json.Marshal(chatRequest{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   false,
		Format:   req.Format,
		Options:  chatOpts{Temperature: req.Temperature, NumCtx: req.ContextTarget, Seed: req.Seed},
	})
	if err != nil {
		return Response{}, fmt.Errorf("llm: marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("llm: building request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		// Distinguish caller cancellation (a deliberate stop) from genuine
		// unreachability; both are loud, only the latter is ErrUnreachable.
		if ctx.Err() != nil {
			return Response{}, fmt.Errorf("llm: chat call canceled: %w", ctx.Err())
		}
		return Response{}, fmt.Errorf("%w: %s: %v", ErrUnreachable, c.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB guard
	if err != nil {
		return Response{}, fmt.Errorf("llm: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("llm: ollama returned %s: %s", resp.Status, snippet(raw))
	}

	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return Response{}, fmt.Errorf("llm: decoding response: %w", err)
	}
	if !cr.Done {
		return Response{}, fmt.Errorf("llm: incomplete response (done=false) from %s", c.baseURL)
	}
	return Response{
		Content:          cr.Message.Content,
		PromptTokens:     cr.PromptEvalCount,
		CompletionTokens: cr.EvalCount,
	}, nil
}

// Stream performs one streaming chat completion (Ollama's
// newline-delimited JSON), invoking onToken for each incremental content
// chunk. The returned Response aggregates the full content and the final
// chunk's token counts. A nil onToken is equivalent in result to Chat.
func (c *Client) Stream(ctx context.Context, req Request, onToken func(string)) (Response, error) {
	if req.Model == "" {
		return Response{}, fmt.Errorf("llm: request has no model")
	}
	body, err := json.Marshal(chatRequest{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   true,
		Format:   req.Format,
		Options:  chatOpts{Temperature: req.Temperature, NumCtx: req.ContextTarget, Seed: req.Seed},
	})
	if err != nil {
		return Response{}, fmt.Errorf("llm: marshalling request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("llm: building request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, fmt.Errorf("llm: chat call canceled: %w", ctx.Err())
		}
		return Response{}, fmt.Errorf("%w: %s: %v", ErrUnreachable, c.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return Response{}, fmt.Errorf("llm: ollama returned %s: %s", resp.Status, snippet(raw))
	}

	var content strings.Builder
	var last chatResponse
	dec := json.NewDecoder(resp.Body)
	for {
		var cr chatResponse
		if err := dec.Decode(&cr); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return Response{}, fmt.Errorf("llm: decoding stream: %w", err)
		}
		if cr.Message.Content != "" {
			content.WriteString(cr.Message.Content)
			if onToken != nil {
				onToken(cr.Message.Content)
			}
		}
		last = cr
		if cr.Done {
			break
		}
	}
	return Response{
		Content:          content.String(),
		PromptTokens:     last.PromptEvalCount,
		CompletionTokens: last.EvalCount,
	}, nil
}

// snippet extracts a short failure payload for error messages.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// Ping checks that the Ollama server answers at all. Used at daemon boot
// to fail loudly before jobs queue up against a dead inference backend.
func (c *Client) Ping(ctx context.Context) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/version", nil)
	if err != nil {
		return fmt.Errorf("llm: building ping: %w", err)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("llm: ping canceled: %w", ctx.Err())
		}
		return fmt.Errorf("%w: %s: %v", ErrUnreachable, c.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("llm: ollama ping returned %s", resp.Status)
	}
	return nil
}
