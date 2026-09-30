package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeEmbedServer speaks enough of Ollama's /api/embed surface for tests
// and captures the last request.
func fakeEmbedServer(t *testing.T, status int, resp any) (*httptest.Server, *embedRequest) {
	t.Helper()
	var captured embedRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("fake embed: decoding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(ts.Close)
	return ts, &captured
}

func TestEmbedReturnsOneVectorPerInput(t *testing.T) {
	ts, captured := fakeEmbedServer(t, http.StatusOK, embedResponse{
		Embeddings: [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}},
	})
	c := NewClient(ts.URL, nil)

	got, err := c.Embed(context.Background(), "nomic-embed-text", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != 2 || len(got[0]) != 3 {
		t.Fatalf("Embed returned %dx%d vectors, want 2x3", len(got), len(got[0]))
	}
	if got[1][0] != 0.4 {
		t.Errorf("second vector = %v, want it to start 0.4", got[1])
	}
	if captured.Model != "nomic-embed-text" {
		t.Errorf("model on the wire = %q, want nomic-embed-text", captured.Model)
	}
	if len(captured.Input) != 2 || captured.Input[0] != "alpha" {
		t.Errorf("input on the wire = %v, want [alpha beta]", captured.Input)
	}
}

func TestEmbedRejectsBadInput(t *testing.T) {
	ts, _ := fakeEmbedServer(t, http.StatusOK, embedResponse{})
	c := NewClient(ts.URL, nil)

	if _, err := c.Embed(context.Background(), "", []string{"x"}); err == nil {
		t.Error("Embed with no model succeeded, want an error")
	}
	if _, err := c.Embed(context.Background(), "m", nil); !errors.Is(err, ErrEmptyEmbedInput) {
		t.Errorf("Embed with no input err = %v, want ErrEmptyEmbedInput", err)
	}
}

func TestEmbedRejectsWrongVectorCount(t *testing.T) {
	ts, _ := fakeEmbedServer(t, http.StatusOK, embedResponse{
		Embeddings: [][]float32{{1, 2}}, // one vector for two inputs
	})
	c := NewClient(ts.URL, nil)

	_, err := c.Embed(context.Background(), "m", []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "1 vectors for 2 inputs") {
		t.Fatalf("Embed err = %v, want a vector-count mismatch", err)
	}
}

func TestEmbedNon200IsLoud(t *testing.T) {
	ts, _ := fakeEmbedServer(t, http.StatusInternalServerError, map[string]string{"error": "model not found"})
	c := NewClient(ts.URL, nil)

	_, err := c.Embed(context.Background(), "missing-model", []string{"a"})
	if err == nil {
		t.Fatal("Embed against a 500 succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("Embed err = %v, want it to name the HTTP status", err)
	}
}
