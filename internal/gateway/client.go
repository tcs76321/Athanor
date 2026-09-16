package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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

// Fetch is the §21.5 entry point. See `Client.Fetch`
// for the full flow. M4-T7 (ADR-0019 §3) adds the
// bounded redirect hop loop: the method appends one
// `network` event per hop (for a non-redirect response,
// exactly one event per call, as before) and follows
// 301/302/303 chains with per-hop policy re-evaluation,
// per-hop rate limiting, and a per-hop pinned-IP dial.
func (c *httpClient) Fetch(ctx context.Context, req *http.Request) (*Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.New("gateway: Fetch requires a non-nil request with a URL")
	}
	currentURL := req.URL.String()
	var chain []string
	for hop := 0; ; hop++ {
		res, err := c.fetchOnce(ctx, req, hop, chain)
		if err != nil {
			return nil, err
		}
		next, follow := redirectTarget(req.URL, res)
		if !follow {
			return res, nil
		}
		if hop >= maxRedirectHops {
			return nil, fmt.Errorf("gateway: redirect chain exceeded %d hops: %w", maxRedirectHops, ErrTooManyRedirects)
		}
		chain = append(chain, currentURL)
		// A followed hop is a new GET to the target; the
		// caller's method and body do not carry over
		// (301/302/303 convert to GET per RFC 9110
		// §15.4.3–4, and the gateway is a GET-for-reader
		// surface). 307/308 are returned to the caller
		// unfollowed — redirectTarget only reports
		// 301/302/303.
		req, err = NewRequest(ctx, http.MethodGet, next)
		if err != nil {
			return nil, fmt.Errorf("gateway: redirect target %q: %w", next, err)
		}
		currentURL = next
	}
}

// maxRedirectHops bounds the redirect chain Client.Fetch
// follows (ADR-0019 §3). Each hop re-runs Policy.Eval, the
// per-host rate limiter, and the pinned-IP dial, so every
// containment property (allowlist, rebinding guard, rate
// limit, size cap, audit) holds per hop, not just the
// first.
const maxRedirectHops = 5

// redirectStatuses are the redirect responses the hop
// loop follows. 307/308 are deliberately absent: they
// require method+body preservation, a capability no M4
// consumer needs; the caller receives the redirect
// response unfollowed.
var redirectStatuses = map[int]bool{301: true, 302: true, 303: true}

// NewRequest builds an outbound *http.Request for
// Client.Fetch. It exists so callers outside this package
// (the M4-T7 tool adapter) can construct fetch requests
// without touching http.NewRequestWithContext — Gate G1
// rule 6 keeps outbound-HTTP call sites inside
// internal/gateway (ADR-0017 §2).
func NewRequest(ctx context.Context, method, rawURL string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, method, rawURL, nil)
}

// redirectTarget returns the next-hop URL when res is a
// followed redirect (301/302/303 whose Location resolves
// against base to an http(s) URL with a host). The bool is
// "follow this". Relative Locations are resolved against
// base; a Location that parses to a non-http(s) scheme is
// not followed (the caller receives the redirect response
// as-is, fail-closed).
func redirectTarget(base *url.URL, res *Response) (string, bool) {
	if !redirectStatuses[res.StatusCode] {
		return "", false
	}
	loc := res.Header.Get("Location")
	if loc == "" {
		return "", false
	}
	ref, err := url.Parse(loc)
	if err != nil {
		return "", false
	}
	next := base.ResolveReference(ref)
	if (next.Scheme != "http" && next.Scheme != "https") || next.Host == "" {
		return "", false
	}
	return next.String(), true
}

// fetchOnce is one hop of the Fetch loop: policy eval →
// rate limit → header sanitize → pinned-IP dial → size cap
// → audit event. The per-hop `network` event carries the
// hop index and, from hop 1 on, the accumulated redirect
// chain (ADR-0019 §3).
func (c *httpClient) fetchOnce(ctx context.Context, req *http.Request, hop int, chain []string) (*Response, error) {
	start := c.clock()
	requestID := newRequestID()

	// Step 1: policy evaluation. A policy refusal
	// returns an error and a nil body; the audit
	// row carries the decision.
	policyRes, err := c.policy.Eval(ctx, req.URL.String(), c.resolver)
	if err != nil {
		c.auditDenied(ctx, requestID, req.URL.String(), policyRes, time.Since(start), hop, chain)
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
		ev := rateLimitedEvent(requestID, req.URL.String(), policyRes, hostPort, time.Since(start))
		ev.Hop = hop
		ev.RedirectChain = chain
		c.appendEvent(ctx, ev)
		return nil, fmt.Errorf("gateway: %w: host %q", ErrRateLimited, hostPort)
	}

	// Step 3: header sanitization. The fixed
	// User-Agent is set unconditionally so
	// operators see a consistent client.
	_ = sanitizeRequestHeaders(req.Header)
	req.Header.Set("User-Agent", gatewayUserAgent)

	// Step 4: HTTP fetch with the resolved-IP
	// pinned DialContext. The transport is
	// per-hop: a clone of the configured
	// RoundTripper with a DialContext that
	// dials the IPs Policy.Eval returned for
	// this hop's URL. The clone is local to the
	// hop, so two concurrent Fetches (and the
	// hop loop itself) do not race on shared
	// transport state. CheckRedirect returns
	// ErrUseLastResponse: the standard library
	// never follows a redirect on its own; the
	// Fetch loop is the only follower
	// (ADR-0019 §3).
	transport := c.transportFor(policyRes.ResolvedIPs)
	httpClient := &http.Client{
		Transport:     transport,
		Timeout:       c.timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

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
		ev := errorEvent(requestID, req.URL.String(), policyRes, time.Since(start), httpErr)
		ev.Hop = hop
		ev.RedirectChain = chain
		c.appendEvent(ctx, ev)
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
		ev := errorEvent(requestID, req.URL.String(), policyRes, time.Since(start), readErr)
		ev.Hop = hop
		ev.RedirectChain = chain
		c.appendEvent(ctx, ev)
		return nil, fmt.Errorf("gateway: reading %s: %w", req.URL.String(), readErr)
	}

	// Step 6: audit. The `network` event covers
	// the full outcome of this hop. The
	// Truncated flag is the audit field of
	// interest.
	eventName := string(EventFetched)
	if truncated {
		eventName = string(EventTruncated)
	}
	ev := storeEventFromPolicy(
		requestID, eventName, req.URL.String(), policyRes,
		time.Since(start), httpResp.StatusCode, int64(len(body)), truncated,
	)
	ev.Hop = hop
	ev.RedirectChain = chain
	c.appendEvent(ctx, ev)

	return &Response{
		StatusCode: httpResp.StatusCode,
		Header:     httpResp.Header,
		Body:       body,
		Truncated:  truncated,
		URL:        req.URL.String(),
		Decision:   policyRes,
	}, nil
}

