package gateway

import (
	"fmt"
	"net/url"
	"strings"
)

// SearchHit is one result link extracted from a search engine's
// results page (M4-T7, ADR-0019 §4). The URL is absolute (relative
// links are resolved against the engine page URL); non-http(s)
// targets are dropped. The URL is NOT automatically fetched —
// fetching it is a subsequent envelope-gated fetch_url call.
type SearchHit struct {
	Title   string
	URL     string
	Snippet string
}

// maxHitTitleBytes bounds a hit's title so a pathological results
// page cannot produce enormous strings in the tool response.
const maxHitTitleBytes = 512

// ExtractSearchHits pulls result links out of a search results page's
// sanitized markdown. The input is Reader Mode output (post
// bluemonday + prompt-injection scan), so the links are from content
// that already passed the pipeline; this pass is structural, not a
// security boundary — it drops everything that is not a markdown
// link to an http(s) URL under the engine's site or the wider web.
//
// Parsing is deliberately line-based and bracket-matching rather than
// regex: markdown link titles may contain nested brackets only in
// pathological pages, and a line scanner with strings.Index keeps the
// behavior inspectable. A line yields at most one hit (the first
// link); result pages put one result per line or paragraph, and
// multiple links per line are navigation chrome.
//
// max bounds the result count. An empty result slice is a valid
// outcome (the engine's markup changed — surfaced, not hidden,
// ADR-0019 §4).
func ExtractSearchHits(markdown, baseURL string, max int) ([]SearchHit, error) {
	if max <= 0 {
		return nil, fmt.Errorf("gateway: ExtractSearchHits max must be > 0, got %d", max)
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" {
		return nil, fmt.Errorf("gateway: ExtractSearchHits base URL %q: %w", baseURL, ErrInvalidURL)
	}
	var hits []SearchHit
	seen := map[string]bool{}
	for _, line := range strings.Split(markdown, "\n") {
		if len(hits) >= max {
			break
		}
		open := strings.Index(line, "[")
		if open < 0 {
			continue
		}
		mid := strings.Index(line[open:], "](")
		if mid < 0 {
			continue
		}
		mid += open
		close := strings.Index(line[mid:], ")")
		if close < 0 {
			continue
		}
		close += mid
		title := strings.TrimSpace(line[open+1 : mid])
		rawURL := strings.TrimSpace(line[mid+2 : close])
		if title == "" || rawURL == "" {
			continue
		}
		ref, err := url.Parse(rawURL)
		if err != nil {
			continue
		}
		abs := base.ResolveReference(ref)
		if (abs.Scheme != "http" && abs.Scheme != "https") || abs.Host == "" {
			continue
		}
		absStr := abs.String()
		if seen[absStr] {
			continue
		}
		seen[absStr] = true
		snippet := strings.TrimSpace(line[close+1:])
		snippet = strings.TrimLeft(snippet, " —–-:")
		if len(snippet) > maxHitTitleBytes {
			snippet = snippet[:maxHitTitleBytes]
		}
		if len(title) > maxHitTitleBytes {
			title = title[:maxHitTitleBytes]
		}
		hits = append(hits, SearchHit{Title: title, URL: absStr, Snippet: snippet})
	}
	return hits, nil
}