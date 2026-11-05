package ui

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/operator"
)

type fakeOperator struct {
	status operator.Status
	sets   []opSet
}

type opSet struct {
	mode   operator.Mode
	hold   time.Duration
	reason string
}

func (f *fakeOperator) Status() operator.Status { return f.status }

func (f *fakeOperator) Set(_ context.Context, mode operator.Mode, hold time.Duration, reason string) error {
	f.sets = append(f.sets, opSet{mode: mode, hold: hold, reason: reason})
	return nil
}

func TestDashboardShowsHouseholdMode(t *testing.T) {
	fx := setup(t)
	fx.ui.deps.Operator = &fakeOperator{status: operator.Status{
		Mode: operator.ModeFull, Requested: operator.ModeFull,
		Reason: "manual: user present", Allowed: true, Daydream: true,
		Concurrency: 4, CPUQuota: 1,
	}}
	status, body := get(t, fx.ts.URL+"/ui")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	for _, want := range []string{"Household mode", "daydream on", "CPU 100%", "4 jobs", "Stop Athanor"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

func TestDashboardStoppedShowsResume(t *testing.T) {
	fx := setup(t)
	fx.ui.deps.Operator = &fakeOperator{status: operator.Status{
		Mode: operator.ModeStopped, Requested: operator.ModeStopped,
		Reason: "manual: stopped", Concurrency: 0, CPUQuota: 0,
	}}
	status, body := get(t, fx.ts.URL+"/ui")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if !strings.Contains(body, "Resume") {
		t.Error("stopped dashboard has no Resume control")
	}
	if strings.Contains(body, "Stop Athanor") {
		t.Error("stopped dashboard still offers Stop Athanor")
	}
	if !strings.Contains(body, "daydream off") {
		t.Error("stopped dashboard should show daydream off")
	}
}

func TestModeControls(t *testing.T) {
	fx := setup(t)
	op := &fakeOperator{status: operator.Status{Mode: operator.ModeFull, Requested: operator.ModeFull}}
	fx.ui.deps.Operator = op

	cases := []struct {
		form       url.Values
		wantMode   operator.Mode
		wantHold   time.Duration
		wantReason string
	}{
		{url.Values{"action": {"deep"}}, operator.ModeDeep, 0, ""},
		{url.Values{"action": {"full"}}, operator.ModeFull, 0, ""},
		{url.Values{"action": {"lite"}}, operator.ModeLite, 0, ""},
		{url.Values{"action": {"auto"}}, operator.ModeAuto, 0, ""},
		{url.Values{"action": {"stop"}}, operator.ModeStopped, 0, "operator stop"},
		{url.Values{"action": {"resume"}}, operator.ModeAuto, 0, ""},
		{url.Values{"action": {"use"}, "duration": {"30m"}}, operator.ModeLite, 30 * time.Minute, "using the computer"},
		{url.Values{"action": {"use"}, "duration": {"1h"}}, operator.ModeLite, time.Hour, "using the computer"},
	}
	for _, tc := range cases {
		resp := postForm(t, fx.ts.URL+"/ui/mode", tc.form)
		if resp.StatusCode != 303 {
			t.Fatalf("%v: status = %d, want 303", tc.form, resp.StatusCode)
		}
		got := op.sets[len(op.sets)-1]
		if got.mode != tc.wantMode || got.hold != tc.wantHold || got.reason != tc.wantReason {
			t.Errorf("%v: set = %+v, want %q hold=%s reason=%q",
				tc.form, got, tc.wantMode, tc.wantHold, tc.wantReason)
		}
	}
}

func TestModeControlRejectsUnknownAction(t *testing.T) {
	fx := setup(t)
	fx.ui.deps.Operator = &fakeOperator{}
	resp := postForm(t, fx.ts.URL+"/ui/mode", url.Values{"action": {"party"}})
	if resp.StatusCode == 303 {
		t.Fatal("unknown action redirected as if accepted")
	}
}
