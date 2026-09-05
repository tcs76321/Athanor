package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Client is the §21.5 gateway's outbound HTTP surface
// (M4-T5, ADR-0017). It is the *only* door between the
// Core and the public internet. Consumers take the
// interface, not the concrete type, so:
//
//   - Tests substitute a fake.
//   - T7 (gateway-backed tools) and M6 (cloud-inference
//     mediation) plug in through the same seam.
//
// A Client is safe for concurrent use.
type Client interface {
	// Fetch executes one outbound request through
	// the gateway. The flow is:
	//
	//  1. Policy evaluation (allowlist + denylist
	//     + DNS-rebinding guard via the injected
	//     Resolver).
	//  2. Per-host rate-limit check.
	//  3. Header sanitization (strip deny-listed
	//     names; set fixed User-Agent).
	//  4. HTTP fetch with `DialContext` pinned to
	//     the IPs Policy.Eval returned (the §21.5
	//     DNS-rebinding defense at the TCP layer).
	//  5. Response size cap (streamed truncation;
	//     do not fail).
	//  6. Append one `network` event covering the
	//     outcome.
	//
	// The method returns the raw response (status
	// code, headers, body) plus a `Truncated` flag
	// the T6 Reader Mode layer checks to decide
	// whether to re-fetch with a smaller cap.
	Fetch(ctx context.Context, req *http.Request) (*Response, error)
}

// Response is the gateway's return type. It is a thin
// wrapper over the standard library's `*http.Response`
// that flattens the size-cap truncation decision into a
// single field. The body has been read into memory (the
// T6 use case is "hand the LLM a markdown string," not
// "stream a 1 GiB file").
type Response struct {
	// StatusCode is the HTTP status from the
	// upstream response. 0 when the request did
	// not produce a status (transport error or
	// policy refusal).
	StatusCode int
	// Header is the upstream response's headers,
	// unmodified. T6 may inspect Content-Type to
	// decide on a readability pass.
	Header http.Header
	// Body is the upstream response's body,
	// truncated to MaxResponseBytes if it
	// exceeded that cap. The field is nil for
	// transport errors and policy refusals.
	Body []byte
	// Truncated is true when the response was
	// truncated by the size cap. The §21.5
	// truncation policy is "set the flag, do
	// not fail" (ADR-0017 §7).
	Truncated bool
	// URL is the original request URL (echoed
	// back for the caller's logging).
	URL string
	// Decision is the policy decision that
	// permitted the request. Useful for the
	// audit log; callers rarely need it.
	Decision EvalResult
}

// Options configures an httpClient. All fields except
// Logger and Events are required.
type Options struct {
	// Policy is the §21.5 decision function. The
	// client calls Policy.Eval on every Fetch.
	// Required.
	Policy *Policy
	// Resolver is the DNS surface the policy
	// uses for the DNS-rebinding guard. The
	// production wire is `(*net.Resolver).LookupIPAddr`;
	// tests inject a hand-written map. Required.
	Resolver Resolver
	// Events is the audit-log surface. The
	// `network` event category is appended on
	// every Fetch outcome. Optional: a nil
	// Events means "no audit" (the unit tests
	// use this; production wires *store.Store).
	Events EventLogger
	// Logger is the slog logger. Optional: nil
	// → slog.Default().
	Logger *slog.Logger
	// RatePerMinute is the per-host rate-limit
	// capacity. The shipped default is 30
	// (mirrors the §21.5 example config).
	// Required (must be > 0).
	RatePerMinute int
	// MaxResponseBytes is the streamed
	// response-size cap. The shipped default
	// is 10 MiB (mirrors the §21.5 example
	// config). Required (must be > 0).
	MaxResponseBytes int64
	// Timeout is the per-request timeout. The
	// shipped default is 30s. Optional: zero
	// → 30s.
	Timeout time.Duration
	// Clock is the time source. Optional: nil
	// → time.Now. Tests inject a deterministic
	// function.
	Clock func() time.Time
	// RoundTripper is the per-request transport.
	// Optional: nil → http.DefaultTransport. The
	// field exists for tests that want to wire
	// their own transport; production uses the
	// default.
	RoundTripper http.RoundTripper
}

// httpClient is the production Client. It owns the
// per-host rate limiter and the audit-log seam; the
// policy is held by reference (Policy is a value type
// but the httpClient does not mutate it).
type httpClient struct {
	policy           *Policy
	resolver         Resolver
	events           EventLogger
	logger           *slog.Logger
	maxResponseBytes int64
	timeout          time.Duration
	rateLimiter      *hostRateLimiter
	clock            func() time.Time
	roundTripper     http.RoundTripper
}

// NewClient constructs the production Client. The
// constructor validates the Options and wires the
// per-host rate limiter. The `Clock`, `Logger`, and
// `RoundTripper` fields are optional; the rest are
// required.
func NewClient(opts Options) (Client, error) {
	if opts.Policy == nil {
		return nil, errors.New("gateway: Options.Policy is required")
	}
	if opts.Resolver == nil {
		return nil, errors.New("gateway: Options.Resolver is required")
	}
	if opts.RatePerMinute <= 0 {
		return nil, fmt.Errorf("gateway: Options.RatePerMinute must be > 0, got %d", opts.RatePerMinute)
	}
	if opts.MaxResponseBytes <= 0 {
		return nil, fmt.Errorf("gateway: Options.MaxResponseBytes must be > 0, got %d", opts.MaxResponseBytes)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	rt := opts.RoundTripper
	if rt == nil {
		rt = http.DefaultTransport
	}
	return &httpClient{
		policy:           opts.Policy,
		resolver:         opts.Resolver,
		events:           opts.Events,
		logger:           opts.Logger,
		maxResponseBytes: opts.MaxResponseBytes,
		timeout:          timeout,
		rateLimiter:      newHostRateLimiter(clock, float64(opts.RatePerMinute), float64(opts.RatePerMinute)/60.0),
		clock:            clock,
		roundTripper:     rt,
	}, nil
}

// Fetch is the §21.5 single-request entry point. See
// `Client.Fetch` for the full flow. The method appends
// exactly one `network` event per call regardless of
// outcome (success, refusal, truncation, transport
// failure).
func (c *httpClient) Fetch(ctx context.Context, req *http.Request) (*Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.New("gateway: Fetch requires a non-nil request with a URL")
	}
	start := c.clock()
	requestID := newRequestID()

	// Step 1: policy evaluation. A policy refusal
	// returns an error and a nil body; the audit
	// row carries the decision.
	policyRes, err := c.policy.Eval(ctx, req.URL.String(), c.resolver)
	if err != nil {
		c.auditDenied(ctx, requestID, req.URL.String(), policyRes, time.Since(start))
		return nil, err
	}

	// Step 2: per-host rate limit. The bucket key
	// is the URL's host:port (a request to
	// http://example.org:8080 and
	// http://example.org:443 do not share a
	// bucket). The bucket's first call starts it
	// full.
	hostPort := req.URL.Host
	if hostPort == "" {
		hostPort = policyRes.Host
	}
	if !c.rateLimiter.take(hostPort) {
		policyRes.Reason = "rate_limited"
		c.appendEvent(ctx, rateLimitedEvent(requestID, req.URL.String(), policyRes, hostPort, time.Since(start)))
		return nil, fmt.Errorf("gateway: %w: host %q", ErrRateLimited, hostPort)
	}

	// Step 3: header sanitization. The fixed
	// User-Agent is set unconditionally so
	// operators see a consistent client.
	_ = sanitizeRequestHeaders(req.Header)
	req.Header.Set("User-Agent", gatewayUserAgent)

	// Step 4: HTTP fetch with the resolved-IP
	// pinned DialContext. The transport is
	// per-request: a clone of the configured
	// RoundTripper with a DialContext that
	// dials the IPs Policy.Eval returned. The
	// clone is local to this Fetch, so two
	// concurrent Fetches do not race on shared
	// transport state.
	transport := c.transportFor(policyRes.ResolvedIPs)
	httpClient := &http.Client{Transport: transport, Timeout: c.timeout}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		// The caller's context may not have a
		// deadline; the per-request timeout
		// applies on top. context.WithTimeout
		// returns a child context that is
		// cancelled when either the parent or
		// the timeout fires.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	httpResp, httpErr := httpClient.Do(req.WithContext(ctx))
	if httpErr != nil {
		c.appendEvent(ctx, errorEvent(requestID, req.URL.String(), policyRes, time.Since(start), httpErr))
		return nil, fmt.Errorf("gateway: fetch %s: %w", req.URL.String(), httpErr)
	}
	defer func() { _ = httpResp.Body.Close() }()

	// Step 5: response size cap. The cap is a
	// stream-side counter: when the counter
	// exceeds the cap, we stop reading and set
	// the Truncated flag. The fetch does NOT
	// fail. A 50 MiB page with the first 10 MiB
	// containing the answer is useful; a hard
	// fail on size is not.
	body, truncated, readErr := readWithCap(httpResp.Body, c.maxResponseBytes)
	if readErr != nil {
		c.appendEvent(ctx, errorEvent(requestID, req.URL.String(), policyRes, time.Since(start), readErr))
		return nil, fmt.Errorf("gateway: reading %s: %w", req.URL.String(), readErr)
	}

	// Step 6: audit. The `network` event covers
	// the full outcome. The Truncated flag is
	// the audit field of interest.
	eventName := string(EventFetched)
	if truncated {
		eventName = string(EventTruncated)
	}
	c.appendEvent(ctx, storeEventFromPolicy(
		requestID, eventName, req.URL.String(), policyRes,
		time.Since(start), httpResp.StatusCode, int64(len(body)), truncated,
	))

	return &Response{
		StatusCode: httpResp.StatusCode,
		Header:     httpResp.Header,
		Body:       body,
		Truncated:  truncated,
		URL:        req.URL.String(),
		Decision:   policyRes,
	}, nil
}

