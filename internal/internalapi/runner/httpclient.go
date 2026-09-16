// Package runner is the engine's HTTP client for the internal API
// (ROADMAP M2-T4, ADR-0009). The engine holds a *HTTPClient and
// calls RunCode / RunTests exactly as a Job Pod would: same path,
// same body shape, same bearer-token auth. The client is
// loopback-only and stateless.
//
// This package lives under internal/internalapi/runner (not in
// internal/engine) so the engine does not import the internalapi
// package, which would create an import cycle (internalapi does
// not import engine; engine calls the API as a peer).
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/tcs76321/athanor/internal/jobpod"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// TokenLookup is the surface HTTPClient needs to retrieve the
// per-job bearer token at call time. *jobpod.Manager satisfies
// it via TokenFor. The lookup is per-call (not cached) so a
// token rotation on the daemon side is observed immediately.
type TokenLookup interface {
	// TokenFor returns the active per-job token, or
	// jobpod.ErrNotFound if the job has no active pod. The
	// returned string is the secret itself; the caller must
	// not log it.
	TokenFor(jobID string) (string, error)
}

// HTTPClient is the production ToolRunner impl. It POSTs to the
// daemon's own internal API as a Job Pod would. The client is
// safe for concurrent use.
type HTTPClient struct {
	// baseURL is the loopback base, e.g. "http://127.0.0.1:7420".
	// No trailing slash.
	baseURL string
	// tokens is the per-job bearer-token source.
	tokens TokenLookup
	// httpClient is the underlying *http.Client. A zero-value
	// client is fine for loopback HTTP; we keep it as a field
	// so a test can inject a transport without rewriting the
	// whole client.
	httpClient *http.Client
}

// New builds an HTTPClient. baseURL is the daemon's loopback
// HTTP base (e.g. "http://127.0.0.1:7420"); tokens is the
// per-job bearer-token source (typically *jobpod.Manager).
//
// The returned client uses a default *http.Client with a 5s
// connect timeout. Callers that need a custom timeout should
// pass it through context on each call.
func New(baseURL string, tokens TokenLookup) *HTTPClient {
	return &HTTPClient{
		baseURL: baseURL,
		tokens:  tokens,
		httpClient: &http.Client{
			// 5s connect timeout. The per-call read timeout
			// is governed by the caller's context; we do not
			// impose a global read deadline because the engine
			// wants the full per-phase budget available.
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout: 5 * time.Second,
				}).DialContext,
			},
		},
	}
}

// RunCode POSTs to /internal/v1/jobs/{id}/execute_code. The
// engine provides the language and code; the response is the
// pod's ExecuteResult-shaped JSON.
func (c *HTTPClient) RunCode(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	req.Tool = toolenvelope.ToolExecuteCode
	return c.post(ctx, jobID, "/execute_code", req)
}

// RunTests POSTs to /internal/v1/jobs/{id}/run_tests. The
// engine provides the command; the response is the pod's
// ExecuteResult-shaped JSON.
func (c *HTTPClient) RunTests(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	req.Tool = toolenvelope.ToolRunTests
	return c.post(ctx, jobID, "/run_tests", req)
}

// FetchURL POSTs to /internal/v1/jobs/{id}/fetch_url (M4-T7,
// ADR-0019 §1). The Core executes the fetch through the §21.5
// gateway; the response is the extracted (or raw-disabled) content.
func (c *HTTPClient) FetchURL(ctx context.Context, jobID string, req toolenvelope.FetchURLRequest) (toolenvelope.FetchURLResponse, error) {
	req.Tool = toolenvelope.ToolFetchURL
	return postTool[toolenvelope.FetchURLResponse](c, ctx, jobID, "/fetch_url", req)
}

// SearchWeb POSTs to /internal/v1/jobs/{id}/search_web (M4-T7,
// ADR-0019 §4). The Core builds the engine URL from the configured
// template and fetches it through the gateway; the response carries
// the extracted result links.
func (c *HTTPClient) SearchWeb(ctx context.Context, jobID string, req toolenvelope.SearchWebRequest) (toolenvelope.SearchWebResponse, error) {
	req.Tool = toolenvelope.ToolSearchWeb
	return postTool[toolenvelope.SearchWebResponse](c, ctx, jobID, "/search_web", req)
}

// postTool is the M4-T7 generic form of post: same token lookup,
// same loopback URL, same bearer header, same 403 → ErrToolDisallowed
// mapping, but a caller-chosen request/response pair (the gateway
// tools return content shapes, not ExecuteResult). The existing
// post stays as-is so the M2-T4 wire contract is untouched.
func postTool[T any](c *HTTPClient, ctx context.Context, jobID, suffix string, req any) (T, error) {
	var zero T
	token, err := c.tokens.TokenFor(jobID)
	if err != nil {
		if errors.Is(err, jobpod.ErrNotFound) {
			return zero, fmt.Errorf("runner: no active pod for job %s: %w", jobID, err)
		}
		return zero, fmt.Errorf("runner: token lookup: %w", err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return zero, fmt.Errorf("runner: marshal request: %w", err)
	}
	url := c.baseURL + "/internal/v1/jobs/" + jobID + suffix
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return zero, fmt.Errorf("runner: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return zero, fmt.Errorf("runner: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden {
		// Disallowed tool. Surface as a typed error so the
		// engine can distinguish "pod says no" from "pod is
		// broken".
		return zero, ErrToolDisallowed
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return zero, fmt.Errorf("runner: pod returned %d: %s", resp.StatusCode, string(raw))
	}
	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return zero, fmt.Errorf("runner: decode result: %w", err)
	}
	return result, nil
}

// post is the shared body of RunCode / RunTests. It serializes
// req, looks up the per-job token, sends a single POST, and
// decodes the result. The caller is responsible for
// per-tool semantics (e.g. Timeout enforcement via context).
func (c *HTTPClient) post(ctx context.Context, jobID, suffix string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	token, err := c.tokens.TokenFor(jobID)
	if err != nil {
		if errors.Is(err, jobpod.ErrNotFound) {
			return toolenvelope.ExecuteResult{}, fmt.Errorf("runner: no active pod for job %s: %w", jobID, err)
		}
		return toolenvelope.ExecuteResult{}, fmt.Errorf("runner: token lookup: %w", err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return toolenvelope.ExecuteResult{}, fmt.Errorf("runner: marshal request: %w", err)
	}
	url := c.baseURL + "/internal/v1/jobs/" + jobID + suffix
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return toolenvelope.ExecuteResult{}, fmt.Errorf("runner: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return toolenvelope.ExecuteResult{}, fmt.Errorf("runner: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden {
		// Disallowed tool. Surface as a typed error so the
		// engine can distinguish "pod says no" from "pod is
		// broken".
		return toolenvelope.ExecuteResult{}, ErrToolDisallowed
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return toolenvelope.ExecuteResult{}, fmt.Errorf("runner: pod returned %d: %s", resp.StatusCode, string(raw))
	}
	var result toolenvelope.ExecuteResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return toolenvelope.ExecuteResult{}, fmt.Errorf("runner: decode result: %w", err)
	}
	return result, nil
}

// ErrToolDisallowed is re-exported from toolenvelope so callers
// of the runner can use a single typed error without importing
// the toolenvelope package. The internal API returns 403 with
// this error text; the runner matches the status code and
// surfaces toolenvelope.ErrToolDisallowed so the engine can
// distinguish "pod says no" from "pod is broken" with errors.Is.
var ErrToolDisallowed = toolenvelope.ErrToolDisallowed
