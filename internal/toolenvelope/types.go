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
	// Files is a multi-file candidate tree (ADR-0065). When non-empty it
	// supersedes Code for execute_code: the pod stages every file under the
	// scratch dir (paths sanitized) and runs the program. Code stays the
	// single-file shorthand so existing callers are unchanged.
	Files []File `json:"files,omitempty"`
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

// ContextSwapRequest is the wire shape for the M5-T3 context_swap tool
// (§10.1, §25; ADR-0021 §10). The Core performs the swap: the active/dormant
// working set lives in the MCE and no Job Pod can see it, so the route is
// envelope-gated and Core-executed exactly like fetch_url.
type ContextSwapRequest struct {
	// Tool names the closed-set tool ("context_swap"). Defense-in-depth
	// double-check, same as ExecuteRequest.Tool.
	Tool Tool `json:"tool"`
	// Scope names the working set whose active chunk is being rotated. It
	// is set server-side to the authenticated job ID; a client value is
	// ignored (isolation).
	Scope string `json:"scope"`
	// ProjectID is the caller job's project, set server-side (a client
	// value is ignored). It lets the Core enforce that the target chunk
	// belongs to this job or its project — a pod cannot read another
	// scope's chunks.
	ProjectID string `json:"project_id,omitempty"`
	// TargetChunkID is the dormant chunk to activate. It is a deterministic
	// chunk handle from the Dormant Index (ADR-0021 §5).
	TargetChunkID string `json:"target_chunk_id"`
}

// ContextSwapResponse reports one swap and carries the loaded chunk's bytes,
// so the caller can place them into the next prompt. FlushedChunkID is empty
// when no chunk was active; NoOp is true when the target was already active.
type ContextSwapResponse struct {
	Scope          string `json:"scope"`
	LoadedChunkID  string `json:"loaded_chunk_id"`
	LoadedBytes    int    `json:"loaded_bytes"`
	LoadedContent  string `json:"loaded_content"`
	FlushedChunkID string `json:"flushed_chunk_id,omitempty"`
	NoOp           bool   `json:"no_op,omitempty"`
}

// QueryMemoryRequest is the wire shape for the M5-T7 query_memory tool
// (§10.2, §25; ADR-0026 §6). The Core executes the search: the MCE's memory
// lives in the Core's SQLite database, which no Job Pod can see, so the
// route is envelope-gated and Core-executed exactly like context_swap.
type QueryMemoryRequest struct {
	// Tool names the closed-set tool ("query_memory"). Defense-in-depth
	// double-check, same as ExecuteRequest.Tool.
	Tool Tool `json:"tool"`
	// Query is the free-form search text. FTS5 operators in it are
	// neutralised server-side (only word tokens survive).
	Query string `json:"query"`
	// Scope names the project and/or job whose memory is searched. Both
	// empty is rejected server-side: an unscoped query would leak every
	// project's memory into one prompt (ADR-0026 §5).
	Scope string `json:"scope"`
	// ProjectID restricts the search to one project. Optional when Scope
	// is set (which defaults to the authenticated job).
	ProjectID string `json:"project_id,omitempty"`
	// TopK caps the returned hits; zero means the daemon default.
	TopK int `json:"top_k,omitempty"`
}

// MemoryHitWire is one retrieved memory entry on the wire. Chunk hits carry
// the Dormant Index metadata and a chunk id the caller may context_swap to;
// memo hits carry the compacted text. Score is the fused BM25+vector
// reciprocal-rank-fusion score; BM25Rank and CosineRank report which signal
// found the hit (0 = that signal did not).
type MemoryHitWire struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"` // "memo" | "chunk"
	Score         float64 `json:"score"`
	BM25Rank      int     `json:"bm25_rank"`
	CosineRank    int     `json:"cosine_rank"`
	SourceRelPath string  `json:"source_relpath,omitempty"`
	Summary       string  `json:"summary,omitempty"`
	LineStart     int     `json:"line_start,omitempty"`
	LineEnd       int     `json:"line_end,omitempty"`
	Content       string  `json:"content,omitempty"`
}

// QueryMemoryResponse is the wire shape the query_memory route returns.
// VectorEnabled reports whether the vector half ran (it is inert until
// context_engine.memory_embedding_model is set).
type QueryMemoryResponse struct {
	Query         string          `json:"query"`
	Scope         string          `json:"scope"`
	VectorEnabled bool            `json:"vector_enabled"`
	Hits          []MemoryHitWire `json:"hits"`
}
