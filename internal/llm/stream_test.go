package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamCollectsTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"Hello "},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"world"},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""},"done":true,"prompt_eval_count":3,"eval_count":2}` + "\n"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	var tokens []string
	res, err := c.Stream(context.Background(), Request{Model: "m"}, func(tok string) { tokens = append(tokens, tok) })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if res.Content != "Hello world" || res.PromptTokens != 3 || res.CompletionTokens != 2 {
		t.Fatalf("response = %+v", res)
	}
	if len(tokens) != 2 || tokens[0] != "Hello " || tokens[1] != "world" {
		t.Fatalf("tokens = %v", tokens)
	}
}
