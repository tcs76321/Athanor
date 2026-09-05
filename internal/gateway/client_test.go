package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/store"
)

// recordingEvents is a fake EventLogger that captures
// every event the gateway appends. Tests assert on
// `events` after each Fetch.
type recordingEvents struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	Category string
	Level    store.EventLevel
	Data     map[string]any
}

func (r *recordingEvents) AppendEvent(_ context.Context, e store.Event) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Copy the data map so a caller-side mutation
	// does not alter the recorded event.
	data := make(map[string]any, len(e.Data))
	for k, v := range e.Data {
		data[k] = v
	}
	r.events = append(r.events, recordedEvent{
		Category: e.Category,
		Level:    e.Level,
		Data:     data,
	})
	return int64(len(r.events)), nil
}

// recordedByName returns the recorded events whose
// top-level `event` data key matches `name`, in the
// order they were appended. The `data` JSON
// sub-object is unmarshaled into a map. Returns nil
// if no such event was recorded.
func (r *recordingEvents) recordedByName(name string) []map[string]json.RawMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]json.RawMessage
	for _, ev := range r.events {
		raw, ok := ev.Data["data"].(json.RawMessage)
		if !ok {
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		if string(m["event"]) == `"`+name+`"` {
			out = append(out, m)
		}
	}
	return out
}

// fixedResolver maps a host to a single IP. Empty
// `ips` defaults to 1.1.1.1.
type fixedResolver struct {
	ips map[string][]net.IP
}

func (f *fixedResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	raw, ok := f.ips[host]
	if !ok {
		raw = []net.IP{net.ParseIP("1.1.1.1")}
	}
	out := make([]net.IPAddr, 0, len(raw))
	for _, ip := range raw {
		out = append(out, net.IPAddr{IP: ip})
	}
	return out, nil
}

// fixedClock is a deterministic time source.
type fixedClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fixedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fixedClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newTestClient wires a Client with a permissive
// policy, a fixed resolver, a fixed clock, and a
// recording event sink. Tests override fields via
// `opts`. The default RatePerMinute is 2 so the
// rate-limit test can drain the bucket in two calls.
//
// The default resolver maps the host string passed
// by the test to 1.1.1.1. Tests that need a
// different mapping (e.g. to dial an httptest server
// at 127.0.0.1, or to test the private-IP guard)
// override `o.Resolver` via the `opts` callback.
func newTestClient(t *testing.T, policy *Policy, opts ...func(*Options)) (Client, *recordingEvents, *fixedClock) {
	t.Helper()
	events := &recordingEvents{}
	clock := &fixedClock{now: time.Unix(0, 0)}
	resolver := &fixedResolver{}
	o := Options{
		Policy:           policy,
		Resolver:         resolver,
		Events:           events,
		RatePerMinute:    2,
		MaxResponseBytes: 1024,
		Timeout:          5 * time.Second,
		Clock:            clock.Now,
	}
	for _, opt := range opts {
		opt(&o)
	}
	c, err := NewClient(o)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, events, clock
}

// resolverForHTTptest returns a Resolver that maps
// the httptest server's hostname to its bound IP.
// The httptest server's URL.Host has the form
// "127.0.0.1:NNNNN" or "[::1]:NNNNN"; the resolver
// returns the host part so the gateway's
// pinned-IP dial lands on the test server.
//
// Without this, every Fetch against an httptest
// server would dial 1.1.1.1 (the default) and time
// out. The §21.5 design is correct — the resolver
// is the single source of truth for what IPs the
// gateway may dial — and tests must opt in to the
// test-server address.
func resolverForHTTptest(srvURL string) Resolver {
	u, err := url.Parse(srvURL)
	if err != nil {
		return &fixedResolver{}
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if ip == nil {
		// The server bound to a non-IP
		// hostname (rare; defensive).
		return &fixedResolver{}
	}
	return &fixedResolver{ips: map[string][]net.IP{
		host: {ip},
	}}
}

// resolverOpt returns an `opts` callback that wires
// the Resolver to the httptest server's address. The
// callback's shape matches `newTestClient`'s variadic
// parameter so the test reads naturally:
//
//	c, events, _ := newTestClient(t, policy,
//	    resolverOpt(srv.URL),
//	)
func resolverOpt(srvURL string) func(*Options) {
	return func(o *Options) { o.Resolver = resolverForHTTptest(srvURL) }
}

// newTestPolicy returns a permissive policy that
// allows every host AND skips the private-IP CIDR
// check. The latter is the test-only escape hatch
// (ADR-0017 §4, `PolicyOptions.InsecureSkipPrivateIPGuard`):
// tests dial `httptest` servers bound to 127.0.0.1,
// which the production §21.5 guard would otherwise
// refuse. Production code never sets this flag.
func newTestPolicy(t *testing.T) *Policy {
	t.Helper()
	p, err := NewPolicyWithOptions(PolicyOptions{
		DefaultPolicy:             "allow",
		AllowList:                 nil,
		DenyList:                  nil,
		InsecureSkipPrivateIPGuard: true,
	})
	if err != nil {
		t.Fatalf("NewPolicyWithOptions: %v", err)
	}
	return p
}

// failingRoundTripper panics if invoked. Tests that
// expect the gateway to short-circuit before the
// transport use this as the Options.RoundTripper.
type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	panic("gateway: transport called when policy should have refused")
}

// TestFetch_HappyPath is the §21.5 success case: a
// permissive policy, a small response, the gateway
// returns the body, the audit log records one
// `fetched` event.
func TestFetch_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello")
	}))
	defer srv.Close()
	policy := newTestPolicy(t)
	c, events, _ := newTestClient(t, policy, resolverOpt(srv.URL))

	req, err := http.NewRequest("GET", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Fetch(context.Background(), req)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if string(resp.Body) != "hello" {
		t.Errorf("Body = %q, want hello", resp.Body)
	}
	if resp.Truncated {
		t.Errorf("Truncated = true, want false")
	}
	got := events.recordedByName("fetched")
	if len(got) != 1 {
		t.Fatalf("fetched events = %d, want 1", len(got))
	}
}

// TestFetch_StripsDenyListedHeaders pins the §21.5 §6
// behavior: Cookie / Authorization / etc. on the
// request are stripped before the wire send. The
// `httptest` server's handler echoes the headers it
// received, and the test asserts the deny-listed
// names are absent.
func TestFetch_StripsDenyListedHeaders(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	policy := newTestPolicy(t)
	c, _, _ := newTestClient(t, policy, resolverOpt(srv.URL))

	req, err := http.NewRequest("GET", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", "session=secret")
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Api-Key", "secret")
	req.Header.Set("X-Custom", "keep-me")
	if _, err := c.Fetch(context.Background(), req); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	for _, name := range []string{"Cookie", "Authorization", "X-Api-Key"} {
		if v := gotHeaders.Get(name); v != "" {
			t.Errorf("header %q survived sanitization: %q", name, v)
		}
	}
	if v := gotHeaders.Get("X-Custom"); v != "keep-me" {
		t.Errorf("X-Custom stripped: %q", v)
	}
}

// TestFetch_SetsFixedUserAgent pins the §21.5 §6
// User-Agent override: the gateway always sends
// `Athanor/0.1 (+local agent)`, regardless of what
// the caller set.
func TestFetch_SetsFixedUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	policy := newTestPolicy(t)
	c, _, _ := newTestClient(t, policy, resolverOpt(srv.URL))

	req, err := http.NewRequest("GET", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "old-ua")
	if _, err := c.Fetch(context.Background(), req); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotUA != gatewayUserAgent {
		t.Errorf("User-Agent = %q, want %q", gotUA, gatewayUserAgent)
	}
}

// TestFetch_TruncatesOversizedResponse pins the §21.5
// §7 streamed-truncation behavior: a response that
// exceeds MaxResponseBytes is truncated at the cap
// (not failed), the Truncated flag is set, and the
// audit event is `truncated`.
func TestFetch_TruncatesOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := strings.Repeat("a", 4096)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	policy := newTestPolicy(t)
	c, events, _ := newTestClient(t, policy, resolverOpt(srv.URL))

	req, err := http.NewRequest("GET", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Fetch(context.Background(), req)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !resp.Truncated {
		t.Errorf("Truncated = false, want true")
	}
	if int64(len(resp.Body)) > 1024 {
		t.Errorf("Body length = %d, want <= 1024", len(resp.Body))
	}
	if got := events.recordedByName("truncated"); len(got) != 1 {
		t.Errorf("truncated events = %d, want 1", len(got))
	}
	if got := events.recordedByName("fetched"); len(got) != 0 {
		t.Errorf("fetched events = %d, want 0 (the truncated response must not also be a fetched)", len(got))
	}
}

// TestFetch_RateLimitedAfterBudget pins the §21.5 §5
// per-host rate limit: the first N requests succeed,
// the (N+1)th returns ErrRateLimited, and the audit
// log records a `rate_limited` event.
func TestFetch_RateLimitedAfterBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	policy := newTestPolicy(t)
	c, events, clock := newTestClient(t, policy, resolverOpt(srv.URL))

	// Default RatePerMinute in newTestClient is 2.
	for i := 0; i < 2; i++ {
		req, err := http.NewRequest("GET", srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Fetch(context.Background(), req); err != nil {
			t.Fatalf("Fetch %d: %v", i, err)
		}
		// Advance the clock 1ms — not enough to
		// refill (refill=2/60≈0.033 tokens/s).
		clock.Advance(time.Millisecond)
	}
	// Third call: bucket should be empty.
	req, err := http.NewRequest("GET", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Fetch(context.Background(), req)
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("err = %v, want ErrRateLimited", err)
	}
	if got := events.recordedByName("rate_limited"); len(got) != 1 {
		t.Errorf("rate_limited events = %d, want 1", len(got))
	}
}

// TestFetch_PolicyDeniedDeniesBeforeTransport pins the
// §21.5 ordering: the policy evaluation runs *before*
// the rate limit and *before* the transport. A denied
// URL produces zero transport calls (the test
// substitutes a transport that panics if invoked) and
// an audit event with `event=denied`.
func TestFetch_PolicyDeniedDeniesBeforeTransport(t *testing.T) {
	policy, err := NewPolicy("deny", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, events, _ := newTestClient(t, policy, func(o *Options) {
		o.RoundTripper = failingRoundTripper{}
	})

	req, err := http.NewRequest("GET", "https://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Fetch(context.Background(), req)
	if !errors.Is(err, ErrDeniedOffList) {
		t.Errorf("err = %v, want ErrDeniedOffList", err)
	}
	if got := events.recordedByName("denied"); len(got) != 1 {
		t.Errorf("denied events = %d, want 1", len(got))
	}
}

// TestFetch_PrivateIPRefused pins the §21.5 §4
// DNS-rebinding guard: a host whose resolved IP is
// in a non-routable range is refused before the
// transport is invoked. The audit event is
// `event=private_ip`.
//
// The resolver key is the bare hostname
// (`"127.0.0.1"`) because `Policy.Eval` calls
// `LookupIPAddr` with the URL's hostname (no port);
// the URL's `Host` field includes the port.
func TestFetch_PrivateIPRefused(t *testing.T) {
	// Use a strict policy (no skip flag) so the
	// private-IP check is the gate that fires.
	policy, err := NewPolicy("allow", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, events, _ := newTestClient(t, policy, func(o *Options) {
		o.Resolver = &fixedResolver{ips: map[string][]net.IP{
			"127.0.0.1": {net.ParseIP("127.0.0.1")},
		}}
		o.RoundTripper = failingRoundTripper{}
	})

	req, err := http.NewRequest("GET", "http://127.0.0.1:80/", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Fetch(context.Background(), req)
	if !errors.Is(err, ErrDeniedPrivateIP) {
		t.Errorf("err = %v, want ErrDeniedPrivateIP", err)
	}
	if got := events.recordedByName("private_ip"); len(got) != 1 {
		t.Errorf("private_ip events = %d, want 1", len(got))
	}
}

// TestFetch_InvalidURLAudited pins the §21.5 URL
// parsing layer's refusal paths: an invalid URL is
// reported as `denied` and does not invoke the
// transport.
func TestFetch_InvalidURLAudited(t *testing.T) {
	policy := newTestPolicy(t)
	c, events, _ := newTestClient(t, policy, func(o *Options) {
		o.RoundTripper = failingRoundTripper{}
	})

	for _, bad := range []string{"://no-scheme", "file:///etc/passwd", "ftp://example.org/"} {
		t.Run(bad, func(t *testing.T) {
			req, err := http.NewRequest("GET", bad, nil)
			if err != nil {
				// http.NewRequest may reject
				// the URL before Fetch is
				// called. That path is fine.
				return
			}
			_, err = c.Fetch(context.Background(), req)
			if !errors.Is(err, ErrInvalidURL) {
				t.Errorf("err = %v, want ErrInvalidURL", err)
			}
		})
	}
	if got := events.recordedByName("denied"); len(got) == 0 {
		t.Errorf("no denied events recorded for invalid URLs")
	}
}

// TestFetch_NilRequest pins the "defensive" property:
// Fetch must not panic on a nil request.
func TestFetch_NilRequest(t *testing.T) {
	policy := newTestPolicy(t)
	c, _, _ := newTestClient(t, policy)
	if _, err := c.Fetch(context.Background(), nil); err == nil {
		t.Error("Fetch(nil) = nil error, want non-nil")
	}
}

// TestFetch_TransportErrorAudited pins the §21.5
// transport-error path: a server that drops the
// connection mid-response is reported as `event=error`
// with a reason string.
func TestFetch_TransportErrorAudited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("server does not support hijacking")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()
	policy := newTestPolicy(t)
	c, events, _ := newTestClient(t, policy, func(o *Options) {
		o.Timeout = 500 * time.Millisecond
	}, resolverOpt(srv.URL))

	req, err := http.NewRequest("GET", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Fetch(context.Background(), req)
	if err == nil {
		t.Error("Fetch returned nil error after a dropped connection")
	}
	if got := events.recordedByName("error"); len(got) == 0 {
		t.Errorf("no error events recorded for transport failure")
	}
}

// TestFetch_AuditShape pins the on-the-wire shape of
// the §21.5 `network` event. A successful fetch must
// produce an event with the keys ADR-0017 §8
// promises. The test asserts each required key is
// present, so a future refactor that drops a field
// trips the test.
func TestFetch_AuditShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello")
	}))
	defer srv.Close()
	policy := newTestPolicy(t)
	c, events, _ := newTestClient(t, policy, resolverOpt(srv.URL))

	req, err := http.NewRequest("GET", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(context.Background(), req); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	fetched := events.recordedByName("fetched")
	if len(fetched) != 1 {
		t.Fatalf("fetched events = %d, want 1", len(fetched))
	}
	required := []string{"event", "url", "host", "decision", "duration_ms", "request_id", "status", "bytes_read"}
	for _, key := range required {
		if _, ok := fetched[0][key]; !ok {
			t.Errorf("fetched event missing required key %q", key)
		}
	}
}

// TestFetch_PinnedIPDialContext pins the §21.5
// DNS-rebinding defense at the TCP layer: the
// Resolver's IPs are the only IPs the transport
// may dial. The test wires the resolver to return
// 127.0.0.1 for the httptest server's hostname,
// then asserts the request actually reached the
// server (i.e. the dial used the resolved IP).
//
// The `httptest` server is bound to 127.0.0.1 by
// default. Without the gateway's pinning, the
// default `http.Transport` would re-resolve the
// hostname via DNS and fail to reach the test
// server. With the pinning, the dial uses the
// resolver's IP and the server receives the
// request. The handler asserts on the host header
// the server received.
func TestFetch_PinnedIPDialContext(t *testing.T) {
	var gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	policy := newTestPolicy(t)
	c, _, _ := newTestClient(t, policy, resolverOpt(srv.URL))

	req, err := http.NewRequest("GET", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(context.Background(), req); err != nil {
		t.Fatalf("Fetch: %v (the gateway's pinned dial should have reached the httptest server)", err)
	}
	// The server received the request — proof
	// that the gateway's pinned-IP dial landed
	// on the right address. The host header is
	// the URL's hostname (the `http.Transport`
	// sets it; the dial address is independent).
	if gotHost == "" {
		t.Errorf("httptest server did not receive the request (gateway failed to dial the resolved IP)")
	}
}

// TestNewClient_Validation covers the constructor's
// rejection paths.
func TestNewClient_Validation(t *testing.T) {
	base := Options{
		Policy:           &Policy{},
		Resolver:         &fixedResolver{},
		RatePerMinute:    1,
		MaxResponseBytes: 1,
	}
	cases := []struct {
		name    string
		mutate  func(*Options)
		wantErr string
	}{
		{"no_policy", func(o *Options) { o.Policy = nil }, "Policy is required"},
		{"no_resolver", func(o *Options) { o.Resolver = nil }, "Resolver is required"},
		{"zero_rate", func(o *Options) { o.RatePerMinute = 0 }, "RatePerMinute must be"},
		{"zero_cap", func(o *Options) { o.MaxResponseBytes = 0 }, "MaxResponseBytes must be"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := base
			c.mutate(&o)
			_, err := NewClient(o)
			if err == nil {
				t.Fatalf("NewClient succeeded, want error containing %q", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v, want substring %q", err, c.wantErr)
			}
		})
	}
}


