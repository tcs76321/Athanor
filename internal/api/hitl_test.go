package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/project"
)

type fakeHITL struct {
	pending     []hitl.Request
	decided     hitl.Request
	err         error
	lastAction  string
	lastDefer   time.Duration
	decideCalls int
}

func (f *fakeHITL) Pending(_ context.Context) ([]hitl.Request, error) {
	return f.pending, f.err
}

func (f *fakeHITL) Decide(_ context.Context, _, action, _ string, deferFor time.Duration) (hitl.Request, error) {
	f.decideCalls++
	f.lastAction = action
	f.lastDefer = deferFor
	return f.decided, f.err
}

func TestHITLListAndDecision(t *testing.T) {
	h := newHarness(t)
	fake := &fakeHITL{
		pending: []hitl.Request{{ID: "r1", Type: "task_escalation", Severity: "high", Status: "pending", CreatedAt: time.Now()}},
		decided: hitl.Request{ID: "r1", Type: "task_escalation", Severity: "high", Status: "approved", CreatedAt: time.Now()},
	}
	h.api.SetHITL(fake)

	resp, err := http.Get(h.ts.URL + "/hitl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", resp.StatusCode)
	}
	var list struct {
		Requests []hitlResponse `json:"requests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Requests) != 1 || list.Requests[0].ID != "r1" {
		t.Fatalf("list = %+v", list)
	}

	dec, err := http.Post(h.ts.URL+"/hitl/r1/decision", "application/json",
		strings.NewReader(`{"action":"approve","note":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dec.Body.Close() }()
	if dec.StatusCode != http.StatusOK {
		t.Fatalf("decision status = %d, want 200", dec.StatusCode)
	}
	if fake.lastAction != "approve" {
		t.Errorf("action = %q, want approve", fake.lastAction)
	}
}

func TestHITLDecisionErrors(t *testing.T) {
	h := newHarness(t)
	// 503 when unwired.
	resp, err := http.Post(h.ts.URL+"/hitl/r1/decision", "application/json", strings.NewReader(`{"action":"approve"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unwired status = %d, want 503", resp.StatusCode)
	}

	// 409 on a resolved request; 400 on an unknown action; 400 on a bad defer.
	for _, tc := range []struct {
		err  error
		body string
		want int
	}{
		{hitl.ErrNotPending, `{"action":"approve"}`, http.StatusConflict},
		{hitl.ErrUnknownAction, `{"action":"maybe"}`, http.StatusBadRequest},
		{hitl.ErrNotFound, `{"action":"approve"}`, http.StatusNotFound},
		{nil, `{"action":"defer","defer_for":"nope"}`, http.StatusBadRequest},
	} {
		h.api.SetHITL(&fakeHITL{err: tc.err})
		r, err := http.Post(h.ts.URL+"/hitl/r1/decision", "application/json", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		if r.StatusCode != tc.want {
			t.Errorf("body %s err %v: status = %d, want %d", tc.body, tc.err, r.StatusCode, tc.want)
		}
	}

	// A valid defer passes the duration through.
	fake := &fakeHITL{decided: hitl.Request{ID: "r1", Status: "pending"}}
	h.api.SetHITL(fake)
	r, err := http.Post(h.ts.URL+"/hitl/r1/decision", "application/json",
		strings.NewReader(fmt.Sprintf(`{"action":"defer","defer_for":%q}`, "1h30m")))
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if fake.lastAction != "defer" || fake.lastDefer != 90*time.Minute {
		t.Errorf("defer = %q/%v, want defer/1h30m", fake.lastAction, fake.lastDefer)
	}
}

type fakePusher struct {
	res    hitl.Request
	err    error
	calls  int
	remote string
}

func (f *fakePusher) RequestPush(_ context.Context, _, remote string) (hitl.Request, error) {
	f.calls++
	f.remote = remote
	return f.res, f.err
}

func TestProjectPushRoute(t *testing.T) {
	h := newHarness(t)
	p := createTestProject(t, h)
	fake := &fakePusher{res: hitl.Request{ID: "r1", Type: "git_push", Severity: "high", Status: "pending", CreatedAt: time.Now()}}
	h.api.SetPusher(fake)

	resp, err := http.Post(h.ts.URL+"/projects/"+p.ID+"/push", "application/json",
		strings.NewReader(`{"remote":"upstream"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("push status = %d, want 201", resp.StatusCode)
	}
	var body hitlResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ID != "r1" || body.Type != "git_push" {
		t.Fatalf("body = %+v", body)
	}
	if fake.calls != 1 || fake.remote != "upstream" {
		t.Errorf("pusher calls = %d remote %q", fake.calls, fake.remote)
	}
}

func TestProjectPushErrors(t *testing.T) {
	h := newHarness(t)
	p := createTestProject(t, h)

	// 503: not configured.
	resp, err := http.Post(h.ts.URL+"/projects/"+p.ID+"/push", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unwired status = %d, want 503", resp.StatusCode)
	}

	for _, tc := range []struct {
		err  error
		want int
	}{
		{project.ErrNoRepository, http.StatusConflict},
		{project.ErrNotFound, http.StatusNotFound},
	} {
		h.api.SetPusher(&fakePusher{err: tc.err})
		r, err := http.Post(h.ts.URL+"/projects/"+p.ID+"/push", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		if r.StatusCode != tc.want {
			t.Errorf("err %v: status = %d, want %d", tc.err, r.StatusCode, tc.want)
		}
	}
}
