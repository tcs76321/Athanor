package gateway

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Policy is the §21.5 gateway's pure decision function (M4-T5,
// ADR-0017). It owns the allowlist + denylist configuration, the
// suffix-match evaluator, and the DNS-rebinding guard. It
// performs no I/O of its own; the DNS lookup is injected via
// `Resolver` so the test corpus is deterministic and the
// production code path is the same one the unit tests exercise.
//
// A Policy is a value type. Construction is via `NewPolicy`;
// zero-value Policies are not safe (no allowlist set up).
// `Eval` is safe to call concurrently; the per-host rate
// limiter (T5.2) and the `Client.Fetch` orchestrator (T5.2)
// are the only stateful layers above this one.
type Policy struct {
	// defaultPolicy is the §21.5 default-deny knob. The
	// allowed values are "deny" (the shipped default) and
	// "allow" (an opt-in escape hatch for tests; never
	// used in production). "allow" with an empty
	// allow_list is a configuration smell the ADR
	// documents.
	defaultPolicy string

	// allowList is the set of hostnames (lowercased
	// ASCII) the gateway will permit. Match semantics
	// are described in `matchHost` below.
	allowList []string

	// denyList is the explicit-override set. A host
	// that matches both `allowList` and `denyList` is
	// denied. Use of this field is a smell (ADR-0017
	// §3); it ships because operators will widen the
	// allowlist to escape the smell otherwise.
	denyList []string

	// insecureSkipPrivateIPGuard disables the
	// §21.5 DNS-rebinding guard's CIDR check.
	// Production never sets this. The test suite
	// uses it to dial `httptest` servers bound to
	// 127.0.0.1. The flag's name is the friction:
	// any reader of the code sees the
	// `InsecureSkip` prefix and the `TestOnly`
	// / doc comment, and a future contributor
	// who copies the pattern into production is
	// on notice. ADR-0017 §4 documents the
	// production-only invariant.
	insecureSkipPrivateIPGuard bool
}

// Resolver is the DNS-resolution surface the policy uses to
// evaluate the §21.5 DNS-rebinding guard. *net.Resolver
// satisfies it via a small adapter in T5.2; tests inject
// a hand-written map. The interface is the seam the
// production `Client` (T5.2) plugs into.
type Resolver interface {
	// LookupIPAddr returns the IPs the hostname
	// currently resolves to. The order is not
	// significant; the policy refuses the request if
	// *any* returned IP is in a non-routable range.
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// NewPolicy constructs a Policy from the operator's
// configuration. The arguments mirror `internal/config.Network`
// (`default_policy`, `allow_list`, `deny_list` — the last is
// reserved by the ADR for a future config field; the T5.1
// signature takes it explicitly so T5.2 can wire it from
// `config.go` without a constructor churn). Validation:
//
//   - `defaultPolicy` must be "deny" or "allow".
//   - `allowList` and `denyList` are lowercased; entries
//     containing non-ASCII bytes are rejected with an
//     error (T5 is ASCII-only; IDN is a T6+ concern).
//   - Empty entries in either list are rejected.
func NewPolicy(defaultPolicy string, allowList, denyList []string) (*Policy, error) {
	return NewPolicyWithOptions(PolicyOptions{
		DefaultPolicy: defaultPolicy,
		AllowList:     allowList,
		DenyList:      denyList,
	})
}

// PolicyOptions is the §21.5 policy configuration. The
// `NewPolicy` shorthand constructor is the production
// path; `NewPolicyWithOptions` is the explicit form tests
// use to opt into test-only behavior.
//
// The two fields with `_` suffixes are test-only escape
// hatches. Setting either to `true` is a documented
// configuration smell; the production wire never sets
// them. The long names are the friction the §21.5
// discipline depends on.
type PolicyOptions struct {
	// DefaultPolicy, AllowList, DenyList mirror the
	// positional NewPolicy arguments.
	DefaultPolicy string
	AllowList     []string
	DenyList      []string
	// InsecureSkipPrivateIPGuard disables the
	// §21.5 DNS-rebinding guard's CIDR check.
	// Setting this to true makes a hostname that
	// resolves to 127.0.0.1, 10.0.0.0/8, etc.
	// pass through. The test suite sets this; the
	// production wire never does. A future M4-T8
	// regression test asserts the field is `false`
	// in the production default.
	InsecureSkipPrivateIPGuard bool
}

// NewPolicyWithOptions constructs a Policy from a
// `PolicyOptions` struct. Production code uses
// `NewPolicy` (which calls this with all defaults);
// tests use this directly to set the test-only
// escape hatches.
func NewPolicyWithOptions(o PolicyOptions) (*Policy, error) {
	switch o.DefaultPolicy {
	case "deny", "allow":
	default:
		return nil, fmt.Errorf("gateway: default_policy must be \"deny\" or \"allow\", got %q", o.DefaultPolicy)
	}
	al, err := normalizeHostList(o.AllowList, "allow_list")
	if err != nil {
		return nil, err
	}
	dl, err := normalizeHostList(o.DenyList, "deny_list")
	if err != nil {
		return nil, err
	}
	return &Policy{
		defaultPolicy:             o.DefaultPolicy,
		allowList:                 al,
		denyList:                  dl,
		insecureSkipPrivateIPGuard: o.InsecureSkipPrivateIPGuard,
	}, nil
}

// normalizeHostList lowercases, trims whitespace, and rejects
// empty or non-ASCII entries. The non-ASCII rejection is
// T5's ASCII-only constraint; IDN support is a T6+ concern
// (ADR-0017 §3) that requires the `golang.org/x/net/idn`
// package.
func normalizeHostList(in []string, field string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, raw := range in {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			return nil, fmt.Errorf("gateway: %s contains an empty entry", field)
		}
		// ASCII-only check. T5 ships without IDN; this
		// is the same constraint the allowlist
		// evaluator enforces at request time
		// (ErrIDNNotSupported). Failing closed at
		// config-load time means a typo or stray
		// non-ASCII byte in the operator's config is
		// caught at daemon boot, not at first fetch.
		if !isASCII(entry) {
			return nil, fmt.Errorf("gateway: %s entry %q contains non-ASCII characters (IDN support is a T6+ concern)", field, entry)
		}
		out = append(out, strings.ToLower(entry))
	}
	return out, nil
}

// isASCII reports whether every byte in s is < 0x80. The
// fast path is a single pass; the function is inlinable.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// matchHost reports whether `host` matches the allowlist
// entry `allow` (already lowercased ASCII). The match is:
//
//   - exact: host == allow
//   - subdomain: strings.HasSuffix(host, "." + allow)
//
// The explicit `.` separator in the suffix form is the §21.5
// fix for the `evilwikipedia.org` gotcha: a naive
// `strings.HasSuffix(host, "wikipedia.org")` matches
// `evilwikipedia.org`; the `.` separator does not. The unit
// test in `policy_test.go` pins the gotcha as a table row
// so a future refactor cannot silently regress.
func matchHost(host, allow string) bool {
	if host == allow {
		return true
	}
	return strings.HasSuffix(host, "."+allow)
}

// EvalResult is the decision a Policy reaches on a URL. The
// `Decision` field is the human-readable class; the
// `ResolvedIPs` field is populated only when `Decision ==
// DecisionAllowed` (the gateway must pin the IPs into the
// http.Transport.DialContext) or `Decision ==
// DecisionDeniedPrivateIP` (the policy ran the resolver,
// found a bad IP, and refused).
type EvalResult struct {
	// Decision is the policy's verdict on the request.
	// See the Decision* constants below.
	Decision Decision
	// Host is the lowercased ASCII hostname the
	// decision was reached on (after URL parsing).
	Host string
	// ResolvedIPs is the set of IPs the resolver
	// returned, when the policy consulted the resolver.
	// Empty when the request was denied before
	// resolution (off-list, denylist, invalid URL,
	// IDN).
	ResolvedIPs []net.IP
	// Reason is a stable, machine-readable explanation
	// of the decision. Suitable for inclusion in the
	// `network` event's `data` payload. Examples:
	// "allowed", "denied_off_list", "denied_denylist",
	// "denied_idn", "denied_private_ip", "invalid_url".
	Reason string
}

// Decision is the enumerated verdict. The numeric ordering
// is significant: a hypothetical future "summary" function
// could `max(...)` over decisions to produce a worst-case
// verdict. The current call sites branch on the constants
// explicitly; the ordering is for future use.
type Decision int

const (
	// DecisionAllowed is the only Decision that permits
	// the request to proceed. ResolvedIPs is populated
	// with the IPs the gateway must pin into the
	// http.Transport.DialContext.
	DecisionAllowed Decision = iota
	// DecisionDeniedOffList is returned when the host
	// is not in allow_list and defaultPolicy == "deny".
	DecisionDeniedOffList
	// DecisionDeniedDenyList is returned when the host
	// matched an allowlist entry but is also in
	// deny_list.
	DecisionDeniedDenyList
	// DecisionDeniedPrivateIP is returned when the
	// resolver returned at least one non-routable IP
	// (RFC 1918, loopback, link-local, CGN, multicast,
	// unspecified, documentation/reserved).
	DecisionDeniedPrivateIP
	// DecisionDeniedIDN is returned when the host
	// contains non-ASCII bytes. T5 is ASCII-only.
	DecisionDeniedIDN
	// DecisionInvalidURL is returned when the URL is
	// malformed, has a non-http(s) scheme, or has no
	// host.
	DecisionInvalidURL
)

// Eval evaluates the policy on a URL. It parses the URL,
// extracts the host, runs the allowlist + denylist check,
// and (if the request is otherwise permitted) consults the
// resolver to evaluate the DNS-rebinding guard.
//
// The function does not perform network I/O of its own.
// The resolver is injected; the only synchronous work is
// URL parsing, the allowlist/denylist loop, and the
// per-IP CIDR check on the resolver's result.
//
// The function is safe to call concurrently.
func (p *Policy) Eval(ctx context.Context, rawURL string, r Resolver) (EvalResult, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return EvalResult{Decision: DecisionInvalidURL, Reason: "invalid_url"}, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return EvalResult{Decision: DecisionInvalidURL, Reason: "invalid_url"}, fmt.Errorf("%w: scheme must be http or https, got %q", ErrInvalidURL, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return EvalResult{Decision: DecisionInvalidURL, Reason: "invalid_url"}, fmt.Errorf("%w: URL has no host", ErrInvalidURL)
	}
	// ASCII-only constraint. IDN is a T6+ concern; the
	// policy fails closed today so an IDN URL the
	// operator hasn't pre-punycoded is a denial, not a
	// silent allow.
	if !isASCII(host) {
		return EvalResult{Decision: DecisionDeniedIDN, Host: host, Reason: "denied_idn"}, ErrIDNNotSupported
	}
	host = strings.ToLower(host)

	// Deny list is checked first so a denylist entry
	// that also matches an allowlist entry is denied
	// (explicit override wins). The `matchHost` form is
	// used for both lists.
	for _, deny := range p.denyList {
		if matchHost(host, deny) {
			return EvalResult{Decision: DecisionDeniedDenyList, Host: host, Reason: "denied_denylist"}, ErrDeniedDenyList
		}
	}
	// Allowlist. When defaultPolicy is "deny" (the
	// shipped default) and the host is not in the
	// allowlist, the request is refused. When
	// defaultPolicy is "allow" and the allowlist is
	// empty, every host is permitted (this is the
	// opt-in escape hatch; documented smell in
	// ADR-0017 §3).
	allowed := p.defaultPolicy == "allow" && len(p.allowList) == 0
	if !allowed {
		for _, a := range p.allowList {
			if matchHost(host, a) {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return EvalResult{Decision: DecisionDeniedOffList, Host: host, Reason: "denied_off_list"}, ErrDeniedOffList
	}

	// At this point the host is permitted. The
	// DNS-rebinding guard is the next check: the
	// resolver returns the current IPs, and any
	// non-routable IP is a refusal. The guard is the
	// §21.5 SSRF defense (ADR-0017 §4).
	addrs, err := r.LookupIPAddr(ctx, host)
	if err != nil {
		return EvalResult{Decision: DecisionDeniedPrivateIP, Host: host, Reason: "resolver_error"}, fmt.Errorf("gateway: resolving %q: %w", host, err)
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	for _, ip := range ips {
		if isNonRoutable(ip) {
			if p.insecureSkipPrivateIPGuard {
				// Test-only escape hatch.
				// Production never sets
				// this; see ADR-0017 §4.
				continue
			}
			return EvalResult{Decision: DecisionDeniedPrivateIP, Host: host, ResolvedIPs: ips, Reason: "denied_private_ip"}, ErrDeniedPrivateIP
		}
	}
	return EvalResult{Decision: DecisionAllowed, Host: host, ResolvedIPs: ips, Reason: "allowed"}, nil
}
