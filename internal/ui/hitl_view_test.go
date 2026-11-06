package ui

import (
	"testing"
	"time"
)

func TestHITLFieldsOrdersKnownKeysFirst(t *testing.T) {
	fields := hitlFields(`{"zeta":"last","branch":"athanor/p1","reason":"review","alpha":"first"}`)
	if len(fields) != 4 {
		t.Fatalf("len(fields) = %d, want 4", len(fields))
	}
	want := []string{"reason", "branch", "alpha", "zeta"}
	for i, f := range fields {
		if f.Key != want[i] {
			t.Errorf("field[%d].Key = %q, want %q", i, f.Key, want[i])
		}
	}
}

func TestHITLFieldsNestedAndScalarValues(t *testing.T) {
	fields := hitlFields(`{"count":3,"ok":true,"nested":{"a":1}}`)
	if len(fields) != 3 {
		t.Fatalf("len(fields) = %d, want 3", len(fields))
	}
	got := map[string]string{}
	for _, f := range fields {
		got[f.Key] = f.Value
	}
	if got["count"] != "3" {
		t.Errorf("count = %q, want 3", got["count"])
	}
	if got["ok"] != "yes" {
		t.Errorf("ok = %q, want yes", got["ok"])
	}
	if got["nested"] != `{"a":1}` {
		t.Errorf("nested = %q, want compact JSON", got["nested"])
	}
}

func TestHITLFieldsInvalidPayloadIsStillReadable(t *testing.T) {
	fields := hitlFields(`not json`)
	if len(fields) != 1 || fields[0].Key != "payload" || fields[0].Value != "not json" {
		t.Fatalf("fields = %#v, want a single payload row", fields)
	}
	if got := hitlFields("  "); got != nil {
		t.Errorf("empty payload fields = %#v, want nil", got)
	}
}

func TestExpiryLabel(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		exp  *time.Time
		want string
	}{
		{"none", nil, ""},
		{"expired", ptrTime(now.Add(-time.Minute)), "expired"},
		{"soon", ptrTime(now.Add(30 * time.Second)), "expires in under a minute"},
		{"minutes", ptrTime(now.Add(25 * time.Minute)), "expires in 25m"},
		{"hours", ptrTime(now.Add(2*time.Hour + 5*time.Minute)), "expires in 2h 5m"},
		{"days", ptrTime(now.Add(72 * time.Hour)), "expires in 3 days"},
	}
	for _, tc := range cases {
		if got := expiryLabel(tc.exp, now); got != tc.want {
			t.Errorf("%s: expiryLabel = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestHITLTypeLabel(t *testing.T) {
	for typ, want := range map[string]string{
		"git_push":        "Push a branch to a remote",
		"task_escalation": "A task needs a decision",
		"network_access":  "Allow a new source host",
		"custom_future":   "custom_future",
		"":                "Approval request",
	} {
		if got := hitlTypeLabel(typ); got != want {
			t.Errorf("hitlTypeLabel(%q) = %q, want %q", typ, got, want)
		}
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
