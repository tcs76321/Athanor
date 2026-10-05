package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeOllama returns a server speaking enough of the Ollama /api/chat and
// /api/version surface for tests, capturing the last chat request.
func fakeOllama(t *testing.T, status int, resp any) (*httptest.Server, *chatRequest) {
	t.Helper()
	var captured chatRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Errorf("fake ollama: decoding request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(resp)
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"0.0-fake"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts, &captured
}

func okChatResponse() chatResponse {
	return chatResponse{
		Message:         Message{Role: "assistant", Content: "Eureka."},
		Done:            true,
		PromptEvalCount: 42,
		EvalCount:       7,
	}
}

func TestChatSendsPersonaOptions(t *testing.T) {
	ts, captured := fakeOllama(t, http.StatusOK, okChatResponse())
	c := NewClient(ts.URL, nil)

	resp, err := c.Chat(context.Background(), Request{
		Model: "mistral-nemo:12b",
		Messages: []Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello"},
		},
		Temperature:   0.4,
		ContextTarget: 32768,
	})
	if err != nil {
		t.Fatalf("Chat() err = %v, want nil", err)
	}
	if resp.Content != "Eureka." {
		t.Errorf("Content = %q, want %q", resp.Content, "Eureka.")
	}
	if resp.PromptTokens != 42 || resp.CompletionTokens != 7 {
		t.Errorf("token counts = %d/%d, want 42/7", resp.PromptTokens, resp.CompletionTokens)
	}

	// The persona contract (§12): model, temperature, and num_ctx must all
	// be honored in the wire request.
	if captured.Model != "mistral-nemo:12b" {
		t.Errorf("request model = %q, want mistral-nemo:12b", captured.Model)
	}
	if captured.Options.Temperature != 0.4 {
		t.Errorf("request temperature = %v, want 0.4", captured.Options.Temperature)
	}
	if captured.Options.NumCtx != 32768 {
		t.Errorf("request num_ctx = %d, want 32768", captured.Options.NumCtx)
	}
	if captured.Stream {
		t.Error("request asked for streaming, want stream=false")
	}
	if len(captured.Messages) != 2 || captured.Messages[1].Content != "hello" {
		t.Errorf("messages not forwarded verbatim: %+v", captured.Messages)
	}
}

// TestChatForwardsSeedAndFormat (M3-T7.1) pins the wire contract for the
// two new request knobs: a pinned seed appears under options.seed and a
// non-empty format appears at the top level. Each case uses its own fake
// server so the captured request is not polluted by a prior decode
// (json.Decode leaves absent struct fields untouched).
func TestChatForwardsSeedAndFormat(t *testing.T) {
	seed := int64(123456789)
	ts, captured := fakeOllama(t, http.StatusOK, okChatResponse())
	c := NewClient(ts.URL, nil)
	if _, err := c.Chat(context.Background(), Request{
		Model: "qwen3.8:27b-mlx", Messages: []Message{{Role: "user", Content: "judge"}},
		Temperature: 0, ContextTarget: 8192, Seed: &seed, Format: "json",
	}); err != nil {
		t.Fatalf("Chat() err = %v", err)
	}
	if captured.Format != "json" {
		t.Errorf("request format = %q, want json", captured.Format)
	}
	if captured.Options.Seed == nil || *captured.Options.Seed != seed {
		t.Errorf("request seed = %v, want %d", captured.Options.Seed, seed)
	}

	// Unset: a fresh server so the assertion is not masked by the values
	// captured above.
	ts2, captured2 := fakeOllama(t, http.StatusOK, okChatResponse())
	c2 := NewClient(ts2.URL, nil)
	if _, err := c2.Chat(context.Background(), Request{
		Model: "m", Messages: []Message{{Role: "user", Content: "x"}},
	}); err != nil {
		t.Fatalf("Chat() err = %v", err)
	}
	if captured2.Format != nil {
		t.Errorf("unset format = %v, want nil", captured2.Format)
	}
	if captured2.Options.Seed != nil {
		t.Errorf("unset seed = %v, want nil", captured2.Options.Seed)
	}
}

func TestChatUnreachableFailsLoudly(t *testing.T) {
	// Point at a server that is guaranteed down.
	ts := httptest.NewServer(http.NotFoundHandler())
	url := ts.URL
	ts.Close()

	c := NewClient(url, &http.Client{Timeout: 2 * time.Second})
	_, err := c.Chat(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Chat() err = %v, want ErrUnreachable", err)
	}
	if !strings.Contains(err.Error(), url) {
		t.Errorf("error should name the unreachable URL: %v", err)
	}
}

func TestChatServerErrorFailsLoudly(t *testing.T) {
	ts, _ := fakeOllama(t, http.StatusInternalServerError, map[string]string{"error": "model not found"})
	c := NewClient(ts.URL, nil)
	_, err := c.Chat(context.Background(), Request{Model: "missing", Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil {
		t.Fatal("Chat() on HTTP 500 returned nil error")
	}
	if errors.Is(err, ErrUnreachable) {
		t.Errorf("HTTP error should not be classified as unreachable: %v", err)
	}
	if !strings.Contains(err.Error(), "model not found") {
		t.Errorf("error should include the server payload: %v", err)
	}
}

func TestChatIncompleteResponseRejected(t *testing.T) {
	// done=false without streaming is a protocol violation — reject it
	// rather than return half a generation.
	ts, _ := fakeOllama(t, http.StatusOK, chatResponse{Message: Message{Role: "assistant", Content: "partial"}, Done: false})
	c := NewClient(ts.URL, nil)
	if _, err := c.Chat(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}}); err == nil {
		t.Fatal("Chat() accepted done=false response, want error")
	}
}

func TestChatRejectsModellessRequest(t *testing.T) {
	ts, _ := fakeOllama(t, http.StatusOK, okChatResponse())
	c := NewClient(ts.URL, nil)
	if _, err := c.Chat(context.Background(), Request{}); err == nil {
		t.Fatal("Chat() without model accepted, want error")
	}
}

func TestChatContextCancellationIsNotUnreachable(t *testing.T) {
	// A caller canceling (kill switch, shutdown) must surface the
	// cancellation, not be misclassified as an unreachable backend.
	// The handler answers slowly (never blocks forever): the client
	// cancels long before the response arrives.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"late"},"done":true}`))
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	c := NewClient(ts.URL, nil)
	_, err := c.Chat(ctx, Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
	if errors.Is(err, ErrUnreachable) {
		t.Fatalf("cancellation misclassified as unreachable: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestPing(t *testing.T) {
	ts, _ := fakeOllama(t, http.StatusOK, okChatResponse())
	if err := NewClient(ts.URL, nil).Ping(context.Background()); err != nil {
		t.Errorf("Ping(healthy) = %v, want nil", err)
	}

	// Dead server fails with the typed error.
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	if err := NewClient(url, nil).Ping(context.Background()); !errors.Is(err, ErrUnreachable) {
		t.Errorf("Ping(dead) = %v, want ErrUnreachable", err)
	}
}

func TestNewClientTrimsTrailingSlash(t *testing.T) {
	ts, _ := fakeOllama(t, http.StatusOK, okChatResponse())
	c := NewClient(ts.URL+"/", nil)
	if _, err := c.Chat(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}}); err != nil {
		t.Fatalf("Chat() with trailing-slash URL err = %v", err)
	}
}
