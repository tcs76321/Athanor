package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/airlock/scanner"
)

// The M4-T8 behavioral probes (ROADMAP M4-T8). These run against the
// real internet and are gated by ATHANOR_RUN_INTEGRATION=1 — the same
// contract as the M2 Job Pod probes (internal/jobpod/security_test.go):
// they never run in CI (no network); a developer with connectivity
// runs `ATHANOR_RUN_INTEGRATION=1 make test-integration` and the
// reference result is recorded in docs/demo-m4-t8.md.
//
// The unit suites (ssrf_test.go, bypass_test.go,
// hostile_content_test.go) provide the deterministic structural
// guarantee; these probes are the behavioral double-check that the
// same denials hold against real DNS, real TLS, and real servers.

// probeURL is a stable, bare-bones HTML page. example.com is the
// IANA-reserved documentation domain — stable content, no bot
// countermeasures, and honest 200s.
const probeURL = "https://example.com/"

// TestIntegration_GatewayDeniesUnlistedRealDomain proves the shipped
// default (deny + empty allowlist) refuses a real fetch of a real
// domain, and the refusal is audited as denied_off_list under the
// network category. This is the behavioral form of "the gateway is
// closed by default".
func TestIntegration_GatewayDeniesUnlistedRealDomain(t *testing.T) {
	if os.Getenv("ATHANOR_RUN_INTEGRATION") != "1" {
		t.Skip("integration probe; set ATHANOR_RUN_INTEGRATION=1 (needs real network)")
	}
	p, err := NewPolicy("deny", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	events := &recordingEvents{}
	c, err := NewClient(Options{
		Policy:           p,
		Resolver:         &NetworkResolver{},
		Events:           events,
		RatePerMinute:    30,
		MaxResponseBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := NewRequest(context.Background(), "GET", probeURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(context.Background(), req); !errors.Is(err, ErrDeniedOffList) {
		t.Fatalf("err = %v, want ErrDeniedOffList against a real domain", err)
	}
	denied := events.recordedByName("denied")
	if len(denied) != 1 {
		t.Fatalf("denied events = %d, want 1", len(denied))
	}
	var decision, url string
	_ = json.Unmarshal(denied[0]["decision"], &decision)
	_ = json.Unmarshal(denied[0]["url"], &url)
	if decision != "denied_off_list" {
		t.Errorf("audit decision = %q, want denied_off_list", decision)
	}
	if url != probeURL {
		t.Errorf("audit url = %q, want %q", url, probeURL)
	}
}

// TestIntegration_GatewayFetchesAllowlistedRealDomain proves the
// positive path end-to-end against the real internet: allowlisted
// domain → real DNS → real TLS → real HTML → Reader Mode extraction
// → markdown with the applied audit row. The domain must be on the
// probe's allowlist; the extraction assertions are deliberately loose
// (upstream markup can change) — what is pinned is that every layer
// passed and the audit trail is complete.
func TestIntegration_GatewayFetchesAllowlistedRealDomain(t *testing.T) {
	if os.Getenv("ATHANOR_RUN_INTEGRATION") != "1" {
		t.Skip("integration probe; set ATHANOR_RUN_INTEGRATION=1 (needs real network)")
	}
	host := "example.com"
	p, err := NewPolicy("deny", []string{host}, nil)
	if err != nil {
		t.Fatal(err)
	}
	events := &recordingEvents{}
	c, err := NewClient(Options{
		Policy:           p,
		Resolver:         &NetworkResolver{},
		Events:           events,
		RatePerMinute:    30,
		MaxResponseBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(ReaderOptions{
		Enabled:   true,
		Heuristic: scanner.NewPromptInjectionHeuristic(1),
		Events:    events,
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := NewRequest(context.Background(), "GET", probeURL)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Fetch(context.Background(), req)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(resp.URL, host) {
		t.Errorf("URL = %q, want the allowlisted host", resp.URL)
	}
	res, err := r.Extract(context.Background(), resp)
	if err != nil {
		t.Fatalf("Extract: %v (example.com is readable HTML; a failure here is a pipeline regression)", err)
	}
	if res.Markdown == "" {
		t.Error("markdown is empty after a successful fetch + extraction")
	}
	if res.Truncated {
		t.Error("Truncated = true on a ~1 KiB page; the size cap misfired")
	}
	applied := events.recordedByName("reader_mode_applied")
	if len(applied) != 1 {
		t.Fatalf("reader_mode_applied events = %d, want 1", len(applied))
	}
}