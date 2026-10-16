// Conformance scenarios (ADR-0073 §2). The soak holds a live daemon; these
// drive subsystems that unit/integration tests exercise in isolation — the kill
// switch and the UI — end-to-end, and record a pass/fail matrix. The set grows
// incrementally: a scenario is added only when it proves something no existing
// gate already proves. Every scenario is offline (loopback only).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// scenarioResult is one conformance scenario's outcome.
type scenarioResult struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// runScenarios executes the conformance set against a live daemon. A failing
// scenario never aborts the run; it is recorded.
func runScenarios(baseURL string) []scenarioResult {
	return []scenarioResult{
		scenarioFreeze(baseURL),
		scenarioUIPages(baseURL),
		scenarioUISSE(baseURL),
	}
}

// scenarioFreeze drives the §22 kill switch end-to-end: freeze, a reasonless
// unfreeze must be rejected, and a reasoned unfreeze must clear it. It always
// leaves the daemon unfrozen so a later failure cannot wedge the run.
func scenarioFreeze(baseURL string) scenarioResult {
	const name = "freeze-unfreeze"
	defer func() {
		_ = apiCall("DELETE", baseURL+"/freeze", map[string]string{"reason": "soak conformance cleanup"}, nil)
	}()

	if err := apiCall("POST", baseURL+"/freeze", nil, nil); err != nil {
		return scenarioResult{name, false, "freeze: " + err.Error()}
	}
	var st struct {
		Frozen bool `json:"frozen"`
	}
	if err := apiCall("GET", baseURL+"/freeze", nil, &st); err != nil {
		return scenarioResult{name, false, "get freeze: " + err.Error()}
	}
	if !st.Frozen {
		return scenarioResult{name, false, "not frozen after POST /freeze"}
	}
	// §22.2: unfreezing without a reason must be rejected.
	if err := apiCall("DELETE", baseURL+"/freeze", map[string]string{}, nil); err == nil {
		return scenarioResult{name, false, "unfreeze without a reason was accepted"}
	}
	if err := apiCall("DELETE", baseURL+"/freeze", map[string]string{"reason": "soak conformance"}, nil); err != nil {
		return scenarioResult{name, false, "unfreeze with reason: " + err.Error()}
	}
	if err := apiCall("GET", baseURL+"/freeze", nil, &st); err != nil {
		return scenarioResult{name, false, "get freeze after unfreeze: " + err.Error()}
	}
	if st.Frozen {
		return scenarioResult{name, false, "still frozen after a reasoned unfreeze"}
	}
	return scenarioResult{name, true, "freeze → frozen; reasonless unfreeze rejected; reasoned unfreeze → running"}
}

// scenarioUIPages fetches the read-only UI pages and asserts each renders HTML.
func scenarioUIPages(baseURL string) scenarioResult {
	const name = "ui-pages"
	for _, path := range []string{"/ui", "/ui/statistics", "/ui/projects", "/ui/corrections"} {
		resp, err := apiClient.Get(baseURL + path)
		if err != nil {
			return scenarioResult{name, false, path + ": " + err.Error()}
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return scenarioResult{name, false, fmt.Sprintf("%s: HTTP %d", path, resp.StatusCode)}
		}
		if !bytes.Contains(body, []byte("<title>")) {
			return scenarioResult{name, false, path + ": no <title> in body"}
		}
	}
	return scenarioResult{name, true, "dashboard, statistics, projects, corrections render"}
}

// scenarioUISSE opens the dashboard SSE stream and asserts the streaming
// contract (200 + text/event-stream). Reading an event would need live activity;
// the open proves the subscribe path and headers.
func scenarioUISSE(baseURL string) scenarioResult {
	const name = "ui-sse"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(baseURL + "/ui/events")
	if err != nil {
		return scenarioResult{name, false, "open /ui/events: " + err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return scenarioResult{name, false, fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		return scenarioResult{name, false, "content-type " + ct}
	}
	return scenarioResult{name, true, "SSE stream opened (200, text/event-stream)"}
}

// renderConformance renders the conformance matrix section for the report.
func renderConformance(results []scenarioResult) string {
	if len(results) == 0 {
		return ""
	}
	passed := 0
	var b strings.Builder
	b.WriteString("## Conformance (ADR-0073 §2)\n\n")
	b.WriteString("| scenario | result | detail |\n|---|---|---|\n")
	for _, r := range results {
		mark := "FAIL"
		if r.Pass {
			mark = "PASS"
			passed++
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", r.Name, mark, r.Detail)
	}
	fmt.Fprintf(&b, "\n%d/%d scenario(s) passed.\n", passed, len(results))
	return b.String()
}

// loadConformance reads the scenarios.json written by a soak (empty when the
// file is absent, so a non-soak results tree still reports).
func loadConformance(path string) []scenarioResult {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []scenarioResult
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}
