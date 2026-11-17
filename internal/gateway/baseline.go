package gateway

import "strings"

// Profile is a named starting point for the global network policy. Profiles
// are opt-in at onboarding: the shipped default is default-deny with no
// allowlist, and an operator chooses "research" deliberately.
//
// A profile is data, not a hidden policy. The operator can edit the resulting
// allow_list/deny_list in config.yaml, and a project can add a time-bounded
// grant without changing the global profile.
type Profile struct {
	Name        string
	Description string
	Allow       []string
	Deny        []string
}

// baselineDeny is the stable, high-value deny set every profile carries:
// cloud metadata endpoints and local-only names. It is deliberately short.
//
// A bundled list of "known malicious IPs" is not a control we ship: static IP
// lists go stale, produce false positives, and are evaded by a new address.
// The durable controls are the reserved/private CIDR guard (always on), the
// pinned-IP dial, a curated domain denylist, and the optional local
// threat-intel file (`network.blocklist_path`) the operator maintains.
var baselineDeny = []string{
	"localhost",
	"internal", // *.internal
	"local",    // *.local (mDNS)
	"metadata.google.internal",
	"instance-data",
	"169.254.169.254",
	"100.100.100.200",
	"fd00:ec2::254",
}

// researchAllow is the opt-in research profile: search engines plus the
// primary sources most research starts from. Suffix matching means
// `wikipedia.org` also covers `en.wikipedia.org`.
//
// Allowlisting a search engine permits the search call, not the result links:
// each result host is fetched through the same policy, so the operator sees a
// skipped-host list and can grant a result base explicitly.
var researchAllow = []string{
	// Search engines.
	"google.com",
	"duckduckgo.com",
	"bing.com",
	"search.brave.com",
	// Reference and research.
	"wikipedia.org",
	"wikimedia.org",
	"arxiv.org",
	"pubmed.ncbi.nlm.nih.gov",
	"ncbi.nlm.nih.gov",
	"doi.org",
	"nature.com",
	"science.org",
	// Software and documentation.
	"github.com",
	"raw.githubusercontent.com",
	"go.dev",
	"pkg.go.dev",
	"docs.python.org",
	"developer.mozilla.org",
	"nodejs.org",
	"rust-lang.org",
	"crates.io",
	"pypi.org",
	// General news.
	"reuters.com",
	"apnews.com",
	"bbc.co.uk",
	"npr.org",
	"news.ycombinator.com",
}

// Profiles returns the built-in profiles in stable order.
func Profiles() []Profile {
	return []Profile{
		{
			Name:        "local-only",
			Description: "Default-deny. No outbound reads; local sources and the news wire only.",
			Deny:        append([]string(nil), baselineDeny...),
		},
		{
			Name:        "research",
			Description: "Search engines plus common research and documentation sources. Result hosts still need a grant.",
			Allow:       append([]string(nil), researchAllow...),
			Deny:        append([]string(nil), baselineDeny...),
		},
		{
			Name:        "custom",
			Description: "Start from default-deny and the stable metadata/local deny set; add your own allowlist.",
			Deny:        append([]string(nil), baselineDeny...),
		},
	}
}

// ProfileByName resolves a profile name (case-insensitive).
func ProfileByName(name string) (Profile, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, p := range Profiles() {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}
