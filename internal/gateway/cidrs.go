package gateway

import (
	"net"
)

// nonRoutableCIDRs is the §21.5 DNS-rebinding guard's deny-list
// (ADR-0017 §4). A request whose hostname resolves to *any* IP
// in any of these ranges is refused. The list is the standard
// "non-routable" set: RFC 1918 private, loopback, link-local,
// carrier-grade NAT, multicast, unspecified, and the IANA
// documentation/reserved ranges. Together they cover the SSRF
// attack surface: a malicious allowlisted domain that flips its
// DNS to 127.0.0.1, 169.254.169.254 (cloud metadata), or any
// of the metadata / cluster-internal ranges is refused before
// the TCP connect.
//
// The list is built once at package init via `mustParseCIDRs`
// (parse errors are unrecoverable and would mean a typo in this
// file). The CIDR list is then frozen as a slice of
// `*net.IPNet`; the `isNonRoutable` helper iterates the slice.
var nonRoutableCIDRs = mustParseCIDRs([]string{
	// RFC 1918: private use
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	// Loopback
	"127.0.0.0/8",
	"::1/128",
	// Link-local
	"169.254.0.0/16",
	"fe80::/10",
	// Carrier-grade NAT (RFC 6598)
	"100.64.0.0/10",
	// Multicast
	"224.0.0.0/4",
	"ff00::/8",
	// Documentation / reserved (RFC 5735, RFC 6890).
	// These ranges are not routable on the public
	// internet; legitimate public services will never
	// resolve to them. Including them closes the
	// "documentation" SSRF vector.
	"192.0.2.0/24",     // TEST-NET-1
	"198.51.100.0/24",  // TEST-NET-2
	"203.0.113.0/24",   // TEST-NET-3
	"198.18.0.0/15",    // benchmarking
	"192.88.99.0/24",   // 6to4 anycast (deprecated)
	"233.252.0.0/24",   // MCAST-TEST-NET
	// Unspecified
	"0.0.0.0/8",
	"::/128",
})

// mustParseCIDRs parses a slice of CIDR strings into a slice
// of *net.IPNet. A parse error panics at package init time —
// which is the right shape: a typo in the deny-list is a
// package-level bug, not a runtime decision.
func mustParseCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("gateway: invalid CIDR in nonRoutableCIDRs: " + c + ": " + err.Error())
		}
		out = append(out, n)
	}
	return out
}

// isNonRoutable reports whether ip is in any of the
// non-routable CIDR ranges. The function is the §21.5 SSRF
// defense. It is inlinable for hot paths (the per-request
// evaluation loops over resolved IPs; the per-IP check is
// the inner loop).
func isNonRoutable(ip net.IP) bool {
	if ip == nil {
		return true
	}
	for _, n := range nonRoutableCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
