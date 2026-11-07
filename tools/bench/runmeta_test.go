package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRuntime(t *testing.T) {
	cases := []struct {
		explicit string
		external bool
		want     string
		wantErr  bool
	}{
		{"", false, "host", false},
		{"", true, "core-pod", false},
		{"core-pod", true, "core-pod", false},
		{"core-pod", false, "", true},
		{"host", false, "host", false},
		{"host", true, "", true},
		{"cloud", false, "", true},
	}
	for _, tc := range cases {
		got, err := resolveRuntime(tc.explicit, tc.external)
		if tc.wantErr {
			if err == nil {
				t.Errorf("resolveRuntime(%q, %v) = nil error, want error", tc.explicit, tc.external)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveRuntime(%q, %v): %v", tc.explicit, tc.external, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveRuntime(%q, %v) = %q, want %q", tc.explicit, tc.external, got, tc.want)
		}
	}
}

func TestRunMetaRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := runMeta{
		Runtime: "core-pod", Diagnostic: false, Model: "m1", JudgeModel: "j1",
		StartedAt: "2026-11-07T00:00:00Z", Complete: true, Rows: 3,
	}
	if err := writeRunMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	got, err := loadRunMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != "core-pod" || got.Diagnostic || got.Model != "m1" {
		t.Errorf("meta = %+v", got)
	}
	if got.Schema != 1 {
		t.Errorf("schema = %d, want 1", got.Schema)
	}
}

func TestRunHeaderLabelsHostAsDiagnostic(t *testing.T) {
	header := renderRunHeader(runMeta{Runtime: "host", Diagnostic: true, StartedAt: "2026-11-07T00:00:00Z"})
	if !strings.Contains(header, "DIAGNOSTIC") {
		t.Errorf("header = %q, want a DIAGNOSTIC label", header)
	}
	if strings.Contains(header, "HEADLINE") {
		t.Errorf("header = %q, must not claim headline", header)
	}
}

func TestRunMetaJSONIsStable(t *testing.T) {
	dir := t.TempDir()
	if err := writeRunMeta(dir, runMeta{Runtime: "host", Diagnostic: true}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, runMetaFile))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["runtime"] != "host" || raw["diagnostic"] != true {
		t.Errorf("raw = %v", raw)
	}
}
