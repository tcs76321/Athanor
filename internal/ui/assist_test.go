package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/assist"
)

type fakeAssist struct {
	gotField, gotText, gotMode string
	result                     assist.Result
	err                        error
}

func (f *fakeAssist) Refine(ctx context.Context, field, text, mode string) (assist.Result, error) {
	f.gotField, f.gotText, f.gotMode = field, text, mode
	return f.result, f.err
}

func TestAssistHandler(t *testing.T) {
	fa := &fakeAssist{result: assist.Result{
		Text: "A refined goal.", Notes: []string{"clarified the outcome"}, Field: "project.goal", Mode: "improve",
	}}
	u := New(Deps{Assist: fa})
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	body := strings.NewReader(`{"field":"project.goal","text":"rough goal"}`)
	resp, err := http.Post(ts.URL+"/ui/assist", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("cache-control = %q", resp.Header.Get("Cache-Control"))
	}
	var got assist.Result
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Text != "A refined goal." {
		t.Fatalf("result = %+v", got)
	}
	if fa.gotField != "project.goal" || fa.gotText != "rough goal" {
		t.Fatalf("assist call = %+v", fa)
	}
}

func TestAssistHandlerRejectsBadJSON(t *testing.T) {
	u := New(Deps{Assist: &fakeAssist{}})
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/ui/assist", "application/json", strings.NewReader(`{`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
