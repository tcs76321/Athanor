package toolenvelope

// ExecuteRequest is the wire shape the engine sends to the Job Pod
// for an execute_code or run_tests call. The shape is shared
// between the engine (which constructs it) and the internal API
// runner (which serializes it to JSON) so the two stay in lockstep
// without an import cycle on internal/internalapi.
//
// The fields cover the M2-T4 surface. M3-T2 may extend with
// environment variables, working-directory overrides, or a richer
// language enum; new fields must remain backward-compatible (the
// runner ignores unknown fields today).
type ExecuteRequest struct {
	// Tool names the closed-set tool ("execute_code" or "run_tests").
	// The internal API also checks EnvelopeFor(jobID) before
	// dispatching; the Tool field is a defense-in-depth double-check.
	Tool Tool `json:"tool"`
	// Language is "python" for execute_code in M2-T4; ignored by
	// run_tests. The closed set is enforced in the handler.
	Language string `json:"language,omitempty"`
	// Code is the source to execute. Ignored by run_tests.
	Code string `json:"code,omitempty"`
	// Command is the test command line. Ignored by execute_code.
	// Example: "pytest -q".
	Command string `json:"command,omitempty"`
	// TimeoutSeconds caps the wall time inside the pod. The runner
	// enforces it via context.WithTimeout. Zero means "use the
	// per-phase budget default."
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// ExecuteResult is the wire shape the Job Pod returns. ExitCode is
// the program's exit status; -1 means the pod itself failed before
// the program ran (image pull error, language missing, etc.).
//
// DurationMS is wall-clock milliseconds from the runner's
// perspective; it includes pod-internal startup that the program's
// own timing would not.
type ExecuteResult struct {
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMS int64  `json:"duration_ms"`
}

// FetchURLRequest is the wire shape for the M4-T7 fetch_url tool
// (ADR-0019 §1). The Core executes the fetch — Job Pods run with
// --network=none, so the gateway lives server-side and the route is
// envelope-gated: a job with fetch_url in its envelope holds the
// "allowlisted internet" capability.
type FetchURLRequest struct {
	// Tool names the closed-set tool ("fetch_url"). Defense-in-depth
	// double-check, same as ExecuteRequest.Tool.
	Tool Tool `json:"tool"`
	// URL is the absolute http(s) URL to fetch. The gateway Policy
	// re-validates it (allowlist, deny-list, DNS-rebinding guard);
	// this field's only server-side check is scheme/host sanity so
	// obviously malformed input fails fast with a 400.
	URL string `json:"url"`
	// TimeoutSeconds caps the wall time of the fetch (including all
	// redirect hops). Zero means "use the daemon default".
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// FetchURLResponse is the wire shape the fetch_url route returns. Mode
// is "readability" or "plain" when Reader Mode produced the markdown,
// "raw" when Reader Mode was disabled or the content was not
// extractable (markdown is then empty — raw bytes are never returned
// inline; a tool response is prompt material and must have passed the
// injection scan, ADR-0019 §1).
type FetchURLResponse struct {
	StatusCode  int    `json:"status_code"`
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	SiteName    string `json:"site_name,omitempty"`
	Excerpt     string `json:"excerpt,omitempty"`
	Markdown    string `json:"markdown"`
	Mode        string `json:"mode"`
	Truncated   bool   `json:"truncated"`
	ContentType string `json:"content_type,omitempty"`
}

// SearchWebRequest is the wire shape for the M4-T7 search_web tool
// (ADR-0019 §4). Inert until the operator sets
// network.search_engine_url_template; the built engine URL goes
// through the ordinary gateway allowlist.
type SearchWebRequest struct {
	Tool           Tool   `json:"tool"`
	Query          string `json:"query"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// SearchResult is one extracted result link from the engine's results
// page. The URL is NOT automatically fetched; fetching it is a
// subsequent envelope-gated fetch_url call.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

// SearchWebResponse is the wire shape the search_web route returns. An
// empty Results array means the page was fetched and passed the
// pipeline but no result links were extracted (the engine's markup
// changed — surfaced, not hidden).
type SearchWebResponse struct {
	Query   string         `json:"query"`
	Engine  string         `json:"engine,omitempty"`
	Results []SearchResult `json:"results"`
}
