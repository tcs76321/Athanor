package intake

import (
	"net/url"
	"strings"
)

// FilterSources keeps only URLs whose host is on the gateway allowlist. The
// rule matches the gateway: an exact host or a dot-boundary suffix, so
// "wikipedia.org" does not allow "evilwikipedia.org". A nil allowlist is a
// test/no-policy signal and passes the input through; production always
// supplies the configured list, including the empty list (deny all).
func FilterSources(sources, allow []string) []string {
	if allow == nil {
		return sources
	}
	out := make([]string, 0, len(sources))
	for _, raw := range sources {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		for _, entry := range allow {
			entry = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(entry, ".")))
			if entry == "" {
				continue
			}
			if host == entry || strings.HasSuffix(host, "."+entry) {
				out = append(out, raw)
				break
			}
		}
	}
	return out
}
