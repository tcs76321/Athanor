package gateway

import (
	"net/url"
	"path"
	"strings"
	"time"
)

// Scope is one project's slice of network policy. The global policy is the
// ceiling; a scope can add a hard deny and time-bounded allow grants, never
// override a global deny, and never disable the private/metadata guard.
type Scope struct {
	ProjectID string
	// Deny is a project-specific hard deny (host, or suffix with a leading
	// dot). It wins over a global allow and over a grant.
	Deny []string
	// Grants are operator-approved URL bases, normally created from a HITL
	// network-access request.
	Grants []Grant
}

// Grant is one operator-approved URL base: a scheme, an exact host, and a
// path prefix, optionally expiring. Exact-host matching is deliberate: a
// grant for example.com is not a grant for every subdomain. The path prefix
// is cleaned before matching so `/v1/../admin` cannot widen `/v1/`.
type Grant struct {
	Scheme     string
	Host       string
	PathPrefix string
	ExpiresAt  time.Time
	Reason     string
}

// Matches reports whether u is inside the grant at now. An empty ExpiresAt
// means no expiry. Scheme and host are compared case-insensitively; the path
// is cleaned and matched on a segment boundary.
func (g Grant) Matches(u *url.URL, now time.Time) bool {
	if u == nil {
		return false
	}
	if !g.ExpiresAt.IsZero() && !now.Before(g.ExpiresAt) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(g.Scheme), u.Scheme) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(g.Host), u.Hostname()) {
		return false
	}
	prefix := cleanGrantPath(g.PathPrefix)
	if prefix == "/" {
		return true
	}
	requested := cleanGrantPath(u.Path)
	if requested == prefix {
		return true
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return strings.HasPrefix(requested, prefix)
}

// cleanGrantPath normalizes a URL path for prefix matching: absolute, cleaned,
// and with an empty value meaning the site root. Cleaning collapses `..` and
// `.` segments, so a traversal attempt is compared to its real destination.
func cleanGrantPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}
