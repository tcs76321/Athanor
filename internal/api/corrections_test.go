package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/corrections"
)

type fakeCorrections struct {
	created corrections.Record
	list    []corrections.Record
	err     error
	in      corrections.CaptureInput
	status  string
	edit    corrections.EditInput
}

func (f *fakeCorrections) Capture(_ context.Context, in corrections.CaptureInput) (corrections.Record, error) {
	f.in = in
	return f.created, f.err
}

func (f *fakeCorrections) ListByProject(_ context.Context, _ string) ([]corrections.Record, error) {
	return f.list, f.err
}

func (f *fakeCorrections) SetStatus(_ context.Context, _, status string) error {
	f.status = status
	return f.err
}

func (f *fakeCorrections) Update(_ context.Context, _ string, in corrections.EditInput) (corrections.Record, error) {
	f.edit = in
	return f.created, f.err
}

func (f *fakeCorrections) Get(_ context.Context, _ string) (corrections.Record, error) {
	return f.created, f.err
}

func TestCorrectionRoutes(t *testing.T) {
	h := newHarness(t)
	p := createTestProject(t, h)
	rec := corrections.Record{ID: "c1", Category: "style", Severity: "high", Scope: "project",
		UserFeedback: "used global state", DerivedRule: "prefer injection", Status: "active"}
	fake := &fakeCorrections{created: rec, list: []corrections.Record{rec}}
	h.api.SetCorrections(fake)

	// Create (the §18.4 mandatory form).
	resp, err := http.Post(h.ts.URL+"/projects/"+p.ID+"/corrections", "application/json",
		strings.NewReader(`{"source":"user_rejection","category":"style","severity":"high","reason":"used global state","desired_behavior":"prefer injection","scope":"project"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	var body correctionResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ID != "c1" {
		t.Fatalf("body = %+v", body)
	}
	if fake.in.Source != corrections.SourceUserRejection || fake.in.UserFeedback != "used global state" || fake.in.DerivedRule != "prefer injection" {
		t.Errorf("capture input = %+v", fake.in)
	}

	// List.
	lr, err := http.Get(h.ts.URL + "/projects/" + p.ID + "/corrections")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lr.Body.Close() }()
	if lr.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", lr.StatusCode)
	}
	var list struct {
		Corrections []correctionResponse `json:"corrections"`
	}
	if err := json.NewDecoder(lr.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Corrections) != 1 {
		t.Fatalf("list = %+v", list)
	}

	// Mute.
	req, err := http.NewRequest(http.MethodPatch, h.ts.URL+"/corrections/c1",
		strings.NewReader(`{"status":"muted","derived_rule":"do X"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	pr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pr.Body.Close() }()
	if pr.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", pr.StatusCode)
	}
	if fake.status != corrections.StatusMuted {
		t.Errorf("status = %q, want muted", fake.status)
	}
	if fake.edit.DerivedRule != "do X" {
		t.Errorf("edit = %+v, want derived_rule", fake.edit)
	}
}

func TestCorrectionRouteErrors(t *testing.T) {
	h := newHarness(t)
	p := createTestProject(t, h)

	// 503 unwired.
	resp, err := http.Post(h.ts.URL+"/projects/"+p.ID+"/corrections", "application/json",
		strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unwired status = %d, want 503", resp.StatusCode)
	}

	// 400 on incomplete feedback.
	h.api.SetCorrections(&fakeCorrections{err: corrections.ErrIncompleteFeedback})
	resp2, err := http.Post(h.ts.URL+"/projects/"+p.ID+"/corrections", "application/json",
		strings.NewReader(`{"category":"style"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("incomplete status = %d, want 400", resp2.StatusCode)
	}
}
