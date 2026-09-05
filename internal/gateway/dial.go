package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// transportFor returns an http.RoundTripper with a
// DialContext that dials the supplied resolved IPs at
// the address the standard library requests. The
// transport is a clone of the httpClient's configured
// RoundTripper, so the package-level
// `http.DefaultTransport` is never mutated.
//
// When the configured RoundTripper is not an
// `*http.Transport` (e.g. a custom test double), the
// function returns it as-is. In that case the
// DNS-rebinding defense is *not* applied; the
// production wire always uses an `*http.Transport`, so
// this fallback is for tests only and is documented as
// such.
func (c *httpClient) transportFor(ips []net.IP) http.RoundTripper {
	base, ok := c.roundTripper.(*http.Transport)
	if !ok {
		// Custom RoundTripper (test). The
		// DNS-rebinding defense is bypassed; the
		// test is responsible for the dial
		// behavior.
		return c.roundTripper
	}
	clone := base.Clone()
	clone.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialResolvedIPs(ctx, network, addr, ips)
	}
	return clone
}

// dialResolvedIPs dials the given IPs in order at the
// requested `host:port` (the standard library passes
// the URL's host:port in the DialContext call; the
// `ips` are what the §21.5 policy resolved and pinned
// — the function refuses to re-resolve). The
// `network` argument is "tcp" for both http and
// https; TLS happens after dial.
//
// The function is the §21.5 DNS-rebinding defense at
// the TCP layer: a second DNS query mid-request
// cannot return a different IP because we never ask
// again.
func dialResolvedIPs(ctx context.Context, network, addr string, ips []net.IP) (net.Conn, error) {
	if len(ips) == 0 {
		return nil, errNoResolvedIPs
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	var lastErr error
	for _, ip := range ips {
		conn, err := d.DialContext(ctx, network, joinHostPort(ip.String(), addr))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errNoResolvedIPs
	}
	return nil, lastErr
}

// joinHostPort returns "ip:port" for a literal IP. The
// helper exists because `net.JoinHostPort` is the
// correct way to format an IPv6 literal with brackets
// (e.g. `[::1]:443`); using string concatenation
// produces a malformed address.
func joinHostPort(ip, hostPort string) string {
	_, port, splitErr := net.SplitHostPort(hostPort)
	if splitErr != nil {
		// hostPort had no port. Use 443 for
		// IPv6 literals that lost their port
		// (rare; defensive).
		return net.JoinHostPort(ip, "443")
	}
	return net.JoinHostPort(ip, port)
}

// errNoResolvedIPs is the sentinel for "the policy
// returned no resolved IPs to dial." This is a
// misconfiguration of the §21.5 gateway, not a
// transport failure; the caller surfaces it as a fetch
// error.
var errNoResolvedIPs = errors.New("gateway: no resolved IPs from policy")

// NetworkResolver adapts `*net.Resolver` to the
// `Resolver` interface. The production wire is a
// `NetworkResolver{}` (zero value) which delegates
// to `net.DefaultResolver`; tests inject
// hand-written maps.
//
// The `Options.Resolver == nil` case is rejected by
// `NewClient`, so callers that want "use the system
// resolver" must construct a `NetworkResolver`
// explicitly (the zero value is the canonical
// construction).
type NetworkResolver struct {
	// Resolver is the underlying resolver. nil
	// means "use net.DefaultResolver."
	Resolver *net.Resolver
}

// LookupIPAddr implements Resolver.
func (r *NetworkResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	rsv := r.Resolver
	if rsv == nil {
		rsv = net.DefaultResolver
	}
	return rsv.LookupIPAddr(ctx, host)
}
